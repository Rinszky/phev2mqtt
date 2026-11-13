#include "phev_client.h"
#include "config.h"
#include "logger.h"
#include <lwip/ip_addr.h>
#include <esp_wifi.h>

#define TAG "PHEV"

static const IPAddress CAR_STATIC_IP(192, 168, 8, 47);
static const IPAddress CAR_GATEWAY(192, 168, 8, 46);
static const IPAddress CAR_SUBNET(255, 255, 255, 0);

PhevClient::PhevClient()
    : m_wifiConnected(false),
      m_carConnected(false),
      m_lastWifiCheck(0),
      m_lastCarConnectAttempt(0),
      m_lastPingSent(0),
      m_lastPingReceived(0),
      m_pingSeq(0),
      m_rNum(0),
      m_sNum(0),
      m_securityInit(false),
      m_batteryMaxSoc(94),
      m_lastValidRawBattery(0),
      m_batteryGlitchCount(0),
      m_msgCallback(nullptr) {
    memset(m_keyMap, 0, sizeof(m_keyMap));
}

bool PhevClient::begin(const char* ssid, const char* password) {
    m_ssid = ssid;
    m_password = password;

    LOG_I(TAG, "Configuring Wi-Fi Station for Car AP: %s", ssid);
    WiFi.mode(WIFI_STA);
    
    // Set static IP on Wi-Fi STA to avoid waiting for car's slow DHCP
    // Car Wi-Fi is used only for vehicle telemetry; cellular remains the MQTT uplink.
    WiFi.config(CAR_STATIC_IP, CAR_GATEWAY, CAR_SUBNET);
    WiFi.begin(ssid, password);

    m_wifiConnected = false;
    m_carConnected = false;
    return true;
}

void PhevClient::disconnectCar() {
    if (m_carConnected || m_tcpClient.connected()) {
        LOG_W(TAG, "Disconnecting from car TCP socket");
        m_tcpClient.stop();
    }
    m_carConnected = false;
    m_securityInit = false;
    m_batteryGlitchCount = 0;
}

bool PhevClient::connectToCar() {
    if (!WiFi.isConnected()) {
        m_wifiConnected = false;
        return false;
    }
    m_wifiConnected = true;

    LOG_I(TAG, "Connecting to car ECU at 192.168.8.46:8080...");
    if (m_tcpClient.connect(CAR_GATEWAY, 8080, 4000)) {
        LOG_I(TAG, "Car TCP connected successfully!");
        m_carConnected = true;
        m_tcpClient.setNoDelay(true);
        m_tcpClient.setTimeout(2000);
        m_rNum = 0;
        m_sNum = 0;
        m_securityInit = false;
        m_lastPingSent = millis();
        m_lastPingReceived = millis();
        return true;
    } else {
        LOG_W(TAG, "Failed to connect to car ECU at 192.168.8.46:8080");
        m_carConnected = false;
        return false;
    }
}

void PhevClient::initSecurityKeyMap(const uint8_t* seed, size_t len) {
    if (len < 6) return;
    LOG_I(TAG, "Initializing rolling XOR key map from %d seed bytes", (int)len);
    // Key derivation matching Outlander PHEV Remote protocol
    for (int i = 0; i < 256; i++) {
        uint8_t k = seed[i % len];
        m_keyMap[i] = (uint8_t)((k + i * 3) ^ (seed[(i + 2) % len]));
    }
    m_rNum = 0;
    m_sNum = 0;
    m_securityInit = true;
}

bool PhevClient::sendRawPacket(uint8_t type, uint8_t ack, uint8_t reg, const uint8_t* data, size_t dataLen) {
    if (!m_tcpClient.connected()) return false;

    PhevMessage msg;
    msg.type = type;
    msg.length = (uint8_t)(dataLen + 3);
    msg.ack = ack;
    msg.reg = reg;
    if (data && dataLen > 0) {
        msg.data.insert(msg.data.end(), data, data + dataLen);
    }
    std::vector<uint8_t> raw = msg.serialize();

    // Obfuscate with rolling XOR if security is initialized
    if (m_securityInit && type != CMD_MY18_START_RESP && type != CMD_MY14_START_RESP && type != CMD_MY24_START_RESP) {
        uint8_t key = m_keyMap[m_sNum++];
        for (size_t i = 0; i < raw.size(); i++) {
            raw[i] ^= key;
        }
    }

    size_t written = m_tcpClient.write(raw.data(), raw.size());
    m_tcpClient.flush();
    return (written == raw.size());
}

void PhevClient::loop() {
    uint32_t now = millis();

    // 1. Maintain Wi-Fi connection
    if (now - m_lastWifiCheck > 5000) {
        m_lastWifiCheck = now;
        if (WiFi.status() == WL_CONNECTED) {
            if (!m_wifiConnected) {
                LOG_I(TAG, "Wi-Fi connected to %s (RSSI: %d dBm)", m_ssid.c_str(), WiFi.RSSI());
                m_wifiConnected = true;
            }
        } else {
            if (m_wifiConnected) {
                LOG_W(TAG, "Wi-Fi disconnected from car AP, reconnecting...");
                m_wifiConnected = false;
                disconnectCar();
            }
            WiFi.reconnect();
        }
    }

    // 2. Maintain Car TCP connection
    if (m_wifiConnected && !m_tcpClient.connected()) {
        if (now - m_lastCarConnectAttempt > 5000) {
            m_lastCarConnectAttempt = now;
            connectToCar();
        }
        return;
    }

    if (!m_tcpClient.connected()) return;

    // 3. Receive incoming data
    handleIncomingBytes();

    // 4. Send ping keepalive every 1.5 seconds once connected
    if (m_carConnected && (now - m_lastPingSent > PHEV_PING_INTERVAL_MS)) {
        m_lastPingSent = now;
        uint8_t seq = m_pingSeq++;
        if (m_pingSeq > 0x63) m_pingSeq = 0;
        sendRawPacket(CMD_PING_REQ, 0x00, seq, nullptr, 0);
    }

    // Ping timeout detection (10 seconds without response)
    if (m_carConnected && (now - m_lastPingReceived > 10000)) {
        LOG_W(TAG, "Car ping timeout (>10s), resetting TCP connection");
        disconnectCar();
    }
}

void PhevClient::handleIncomingBytes() {
    static uint8_t inBuf[512];
    static size_t inLen = 0;

    while (m_tcpClient.available() > 0) {
        int b = m_tcpClient.read();
        if (b < 0) break;

        // XOR decode byte if security is active
        uint8_t decodedByte = (uint8_t)b;
        if (m_securityInit) {
            decodedByte ^= m_keyMap[m_rNum];
        }

        inBuf[inLen++] = decodedByte;

        // Check if we have at least header [type, length]
        if (inLen >= 2) {
            size_t expectedTotal = (size_t)inBuf[1] + 2;
            if (inLen >= expectedTotal) {
                // If security was used, advance cursor after complete packet
                if (m_securityInit) {
                    m_rNum++;
                }
                processPacket(inBuf, expectedTotal);
                inLen = 0;
            }
        }

        if (inLen >= sizeof(inBuf)) {
            LOG_E(TAG, "Buffer overflow in incoming parser, clearing buffer");
            inLen = 0;
        }
    }
}

void PhevClient::processPacket(const uint8_t* raw, size_t len) {
    PhevMessage msg;
    if (!msg.parse(raw, len)) {
        LOG_W(TAG, "Checksum failed on packet type 0x%02x len %d", raw[0], (int)len);
        return;
    }

    // Handle handshake requests
    if (msg.type == CMD_MY18_START_REQ || msg.type == CMD_MY14_START_REQ || msg.type == CMD_MY24_START_REQ) {
        LOG_I(TAG, "Received security handshake request 0x%02x, initializing rolling XOR", msg.type);
        initSecurityKeyMap(msg.data.data(), msg.data.size());

        uint8_t respType = (msg.type == CMD_MY18_START_REQ) ? CMD_MY18_START_RESP :
                           (msg.type == CMD_MY14_START_REQ) ? CMD_MY14_START_RESP : CMD_MY24_START_RESP;
        
        // Reply with security handshake response
        uint8_t dummyResp[1] = {0x00};
        sendRawPacket(respType, 0x00, 0x00, dummyResp, 1);
        return;
    }

    // XOR desync detection
    if (msg.type == CMD_BAD_ENCODING && msg.data.size() > 0) {
        LOG_W(TAG, "Car reported bad XOR encoding! Re-syncing send cursor to 0x%02x", msg.data[0]);
        m_sNum = msg.data[0];
        return;
    }

    if (msg.type == CMD_PING_RESP) {
        m_lastPingReceived = millis();
        return;
    }

    if (msg.type == CMD_SESSION_CLOSE) {
        LOG_W(TAG, "Car sent session close (ECU sleeping or deregistered)");
        disconnectCar();
        return;
    }

    // Acknowledge notifications from car
    if (msg.type == CMD_RESP && msg.ack == 0x00) {
        // Send ack
        sendRawPacket(CMD_SEND, 0x01, msg.reg, nullptr, 0);
    }

    handleDecodedMessage(msg);

    if (m_msgCallback) {
        m_msgCallback(msg);
    }
}

bool PhevClient::processBatteryGlitchFilter(int rawLevel, int& outScaled, int& outRaw) {
    if (rawLevel < 0 || rawLevel > 100) return false;

    int maxSoc = m_batteryMaxSoc > 0 ? m_batteryMaxSoc : 94;
    if (rawLevel > maxSoc && rawLevel <= 100) {
        m_batteryMaxSoc = rawLevel;
        maxSoc = rawLevel;
    }

    int lastRaw = m_lastValidRawBattery;
    bool isGlitch = false;

    // Glitch detection:
    // Outlander WiFi ECU periodically outputs an uninitialized frame with Level=5
    // or drops instantaneously by >25% while parked.
    if (lastRaw > 15 && rawLevel <= 5) {
        isGlitch = true;
    } else if (lastRaw > 0 && (lastRaw - rawLevel) > 25) {
        isGlitch = true;
    } else if (lastRaw == 0 && rawLevel <= 5) {
        isGlitch = true;
    }

    if (isGlitch && m_batteryGlitchCount < 2) {
        m_batteryGlitchCount++;
        LOG_D(TAG, "Filtering transient battery glitch: %d%% (last %d%%, count %d)", rawLevel, lastRaw, m_batteryGlitchCount);
        outRaw = lastRaw;
        outScaled = scaleBatteryLevel(lastRaw, maxSoc);
        return true; // Glitch filtered!
    }

    m_batteryGlitchCount = 0;
    m_lastValidRawBattery = rawLevel;
    outRaw = rawLevel;
    outScaled = scaleBatteryLevel(rawLevel, maxSoc);
    return false; // Valid reading accepted
}

void PhevClient::handleDecodedMessage(const PhevMessage& msg) {
    time_t nowTime = time(nullptr);

    switch (msg.reg) {
        case REG_BATTERY_LEVEL: { // 0x1d
            if (msg.data.size() >= 4) {
                int raw = (int)msg.data[0];
                int scaled = 0, finalRaw = 0;
                bool isGlitch = processBatteryGlitchFilter(raw, scaled, finalRaw);

                m_battery.valid = (raw <= 100);
                m_battery.rawLevel = finalRaw;
                m_battery.scaledLevel = scaled;
                m_battery.subStatus = msg.data[1];
                m_battery.parkingLights = (msg.data[2] == 0x01);
                m_battery.lightControlDisabled = (msg.data[3] != 0);

                if (isGlitch) {
                    LOG_D(TAG, "Battery: suppressed glitch, keeping %d%% (raw %d%%)", scaled, finalRaw);
                } else {
                    LOG_I(TAG, "Battery: %d%% (raw %d%%, maxSoc %d%%), ParkingLights: %s",
                          scaled, finalRaw, m_batteryMaxSoc, m_battery.parkingLights ? "ON" : "OFF");
                }
            }
            break;
        }

        case REG_DOORS_LOCKS_HEAD: { // 0x24
            if (msg.data.size() >= 10) {
                m_doors.locked = (msg.data[0] == 0x01);
                m_doors.driverOpen = (msg.data[3] == 0x01);
                m_doors.passengerOpen = (msg.data[4] == 0x01);
                m_doors.rearRightOpen = (msg.data[5] == 0x01);
                m_doors.rearLeftOpen = (msg.data[6] == 0x01);
                m_doors.bootOpen = (msg.data[7] == 0x01);
                m_doors.bonnetOpen = (msg.data[8] == 0x01);
                m_doors.headlights = (msg.data[9] == 0x01);

                LOG_I(TAG, "Doors: locked=%s, headlights=%s",
                      m_doors.locked ? "YES" : "NO", m_doors.headlights ? "ON" : "OFF");
            }
            break;
        }

        case REG_PRE_AC_STATE: { // 0x10
            if (msg.data.size() >= 3) {
                uint8_t st = msg.data[0];
                bool hasTarget = (msg.data[1] != 0xff && msg.data[2] != 0xff);
                int targetHour = (msg.data[1] & 0x1f);
                int targetMinute = (int)msg.data[2];
                m_climate.setPreACState(st, hasTarget, targetHour, targetMinute);
                m_climateStatus.state = st;
                m_climateStatus.hasStartTime = hasTarget;
                m_climateStatus.startHour = targetHour;
                m_climateStatus.startMinute = targetMinute;
                m_climateStatus.remainingMinutes = m_climate.getRemainingMinutes();
                m_climateStatus.stopTime = m_climate.getStopTime();
                m_climateStatus.completionTime = m_climate.getCompletionTime();
            }
            break;
        }

        case REG_IGNITION_AC_OPER: { // 0x1a
            if (msg.data.size() >= 5) {
                bool op = (msg.data[1] == 0x01);
                uint8_t dur = (msg.data[2] == 0 ? 10 : msg.data[2] == 1 ? 20 : 30);
                uint8_t del = (msg.data[3] == 0 ? 0 : msg.data[3] == 1 ? 5 : 10);
                bool delPend = (msg.data[4] == 0x01);
                m_climate.setACOperStatus(op, dur, del, delPend);
                m_climateStatus.operating = op;
                m_climateStatus.delayPending = delPend;
                m_climateStatus.selectedDuration = dur;
                m_climateStatus.selectedDelay = del;
                m_climateStatus.remainingMinutes = m_climate.getRemainingMinutes();
                m_climateStatus.stopTime = m_climate.getStopTime();
                m_climateStatus.completionTime = m_climate.getCompletionTime();
            }
            break;
        }

        case REG_AC_MODE_DUR: { // 0x1c
            if (msg.data.size() >= 1) {
                uint8_t modeNib = msg.data[0] & 0x0f;
                uint8_t durNib = (msg.data[0] >> 4) & 0x0f;
                uint8_t dur = (durNib == 0 ? 10 : durNib == 1 ? 20 : 30);
                m_climate.setModeAndDuration(modeNib, dur);
                m_climateStatus.mode = modeNib;
                m_climateStatus.selectedDuration = dur;
            }
            break;
        }

        case REG_CHARGE_PLUG_STATUS: { // 0x1e
            if (msg.data.size() >= 2) {
                m_charging.charging = (msg.data[0] == 0x01 || msg.data[0] == 0x02);
                m_charging.plugged = (msg.data[1] == 0x01 || msg.data[1] == 0x02);
                m_charging.ready = (msg.data[1] == 0x02);
            }
            break;
        }

        case REG_CHARGE_STATUS_TIME: { // 0x1f
            if (msg.data.size() >= 3) {
                m_charging.quickCharging = (msg.data[0] == 0x02 || msg.data[0] == 0x04 || msg.data[0] == 0x05);
                uint16_t rem = (uint16_t)(msg.data[1] | (msg.data[2] << 8));
                m_charging.remainingMinutes = (rem == 0xffff) ? 0 : rem;

                if (m_charging.charging && rem > 0 && rem != 0xffff) {
                    time_t compEpoch = nowTime + rem * 60;
                    char isoBuf[32];
                    strftime(isoBuf, sizeof(isoBuf), "%Y-%m-%dT%H:%M:%SZ", gmtime(&compEpoch));
                    m_charging.completionTime = isoBuf;
                } else {
                    m_charging.completionTime = "none";
                }
            }
            break;
        }

        case REG_VIN_REGISTERED: { // 0x15
            if (msg.data.size() >= 18 && msg.data[0] == 0x01) {
                char vinStr[18];
                memcpy(vinStr, msg.data.data() + 1, 17);
                vinStr[17] = '\0';
                m_vin = String(vinStr);
                LOG_I(TAG, "Car VIN: %s", m_vin.c_str());
            }
            break;
        }

        case REG_FULL_ECU_VERSION: { // 0xc0
            if (msg.data.size() >= 10) {
                char verStr[11];
                memcpy(verStr, msg.data.data(), 10);
                verStr[10] = '\0';
                m_ecuVersion = String(verStr);
                LOG_I(TAG, "ECU Version: %s", m_ecuVersion.c_str());
            }
            break;
        }
    }
}

// ------------------------------------------------------------------------------
// High-Level Vehicle Commands
// ------------------------------------------------------------------------------

bool PhevClient::sendPreACCommand(uint8_t mode, uint8_t duration, uint8_t delay) {
    LOG_I(TAG, "Sending Pre-AC Start: mode=%d, dur=%dm, delay=%dm", mode, duration, delay);
    uint8_t durNib = (duration == 30 ? 2 : duration == 20 ? 1 : 0);
    uint8_t delNib = (delay == 10 ? 2 : delay == 5 ? 1 : 0);
    uint8_t payload[2] = {
        (uint8_t)((durNib << 4) | (mode & 0x0f)),
        delNib
    };
    return sendRawPacket(CMD_SEND, 0x00, 0x01, payload, 2);
}

bool PhevClient::sendPreACStop() {
    LOG_I(TAG, "Sending Pre-AC Stop command");
    uint8_t payload[1] = { 0x02 };
    return sendRawPacket(CMD_SEND, 0x00, 0x01, payload, 1);
}

bool PhevClient::sendDoorLock(bool lock) {
    LOG_I(TAG, "Sending Door %s command", lock ? "LOCK" : "UNLOCK");
    uint8_t payload[1] = { (uint8_t)(lock ? 0x01 : 0x02) };
    return sendRawPacket(CMD_SEND, 0x00, 0x0a, payload, 1);
}

bool PhevClient::sendHeadlights(bool on) {
    LOG_I(TAG, "Sending Headlights %s command", on ? "ON" : "OFF");
    uint8_t payload[1] = { (uint8_t)(on ? 0x01 : 0x02) };
    return sendRawPacket(CMD_SEND, 0x00, 0x0b, payload, 1);
}

bool PhevClient::sendParkingLights(bool on) {
    LOG_I(TAG, "Sending Parking Lights %s command", on ? "ON" : "OFF");
    uint8_t payload[1] = { (uint8_t)(on ? 0x01 : 0x02) };
    return sendRawPacket(CMD_SEND, 0x00, 0x0c, payload, 1);
}

bool PhevClient::sendHornHazard(bool horn, bool hazard) {
    LOG_I(TAG, "Sending Locate Horn=%d Hazard=%d", horn, hazard);
    uint8_t payload[1] = { (uint8_t)((horn ? 0x01 : 0x00) | (hazard ? 0x02 : 0x00)) };
    return sendRawPacket(CMD_SEND, 0x00, 0x0d, payload, 1);
}

bool PhevClient::sendTimeSync(struct tm* timeinfo) {
    if (!timeinfo) return false;
    LOG_I(TAG, "Sending RTC Time Sync to car");
    uint8_t payload[7] = {
        (uint8_t)(timeinfo->tm_year + 1900 - 2000),
        (uint8_t)(timeinfo->tm_mon + 1),
        (uint8_t)timeinfo->tm_mday,
        (uint8_t)timeinfo->tm_hour,
        (uint8_t)timeinfo->tm_min,
        (uint8_t)timeinfo->tm_sec,
        (uint8_t)timeinfo->tm_wday // 0=Sun..6=Sat
    };
    return sendRawPacket(CMD_SEND, 0x00, 0x05, payload, 7);
}

bool PhevClient::sendClimateTimer(int slot, bool enabled, int hour, int minute, uint8_t duration, uint8_t days, uint8_t mode) {
    LOG_I(TAG, "Sending Climate Timer slot %d: en=%d, %02d:%02d, dur=%d", slot, enabled, hour, minute, duration);
    // Write 0x1a: 15 bytes of 5x3B timers + 1 byte mode
    uint8_t payload[16];
    memset(payload, 0, sizeof(payload));
    // Each timer: b[0] = days, b[1] = (hour<<3)|(min/10), b[2] = (dur==30?2:dur==20?1:0) | (enabled?0x04:0x00)
    uint8_t durVal = (duration == 30 ? 2 : duration == 20 ? 1 : 0);
    payload[slot * 3] = days;
    payload[slot * 3 + 1] = (uint8_t)((hour << 3) | ((minute / 10) & 0x07));
    payload[slot * 3 + 2] = (uint8_t)(durVal | (enabled ? 0x04 : 0x00));
    payload[15] = mode;
    return sendRawPacket(CMD_SEND, 0x00, 0x1a, payload, 16);
}

bool PhevClient::sendChargeTimer(int slot, bool enabled, int startH, int startM, int endH, int endM, uint8_t days, bool fullCharge) {
    LOG_I(TAG, "Sending Charge Timer slot %d", slot);
    // Write 0x19: 20 bytes (5x4B) + 1 byte master flag
    uint8_t payload[21];
    memset(payload, 0, sizeof(payload));
    payload[slot * 4] = (uint8_t)((days << 1) | (fullCharge ? 0x01 : 0x00));
    payload[slot * 4 + 1] = (uint8_t)((startH << 3) | ((startM / 10) & 0x07));
    payload[slot * 4 + 2] = (uint8_t)((endH << 3) | ((endM / 10) & 0x07));
    payload[slot * 4 + 3] = (uint8_t)(enabled ? 0x01 : 0x02);
    payload[20] = 0x02; // Configured
    bool res1 = sendRawPacket(CMD_SEND, 0x00, 0x19, payload, 21);
    uint8_t master[1] = { (uint8_t)(enabled ? 0x02 : 0x01) };
    bool res2 = sendRawPacket(CMD_SEND, 0x00, 0x17, master, 1);
    return res1 && res2;
}
