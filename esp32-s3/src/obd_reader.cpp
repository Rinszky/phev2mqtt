#include "obd_reader.h"
#include "logger.h"

#define TAG "OBD"

ObdReader::ObdReader()
#if OBD_BLUETOOTH_ENABLED
    : m_taskHandle(nullptr),
      m_mutex(nullptr),
      m_nominalCapacityAh(38.0f)
#endif
{
}

ObdReader::~ObdReader() {
#if OBD_BLUETOOTH_ENABLED
    if (m_taskHandle) {
        vTaskDelete(m_taskHandle);
        m_taskHandle = nullptr;
    }
    if (m_mutex) {
        vSemaphoreDelete(m_mutex);
        m_mutex = nullptr;
    }
#endif
}

bool ObdReader::begin(const char* deviceName,
                      const char* pin,
                      const char* macAddress,
                      float nominalCapacityAh) {
#if OBD_BLUETOOTH_ENABLED
    m_deviceName = deviceName ? deviceName : "OBDII";
    m_pin = pin ? pin : "1234";
    m_macStr = macAddress ? macAddress : "";
    m_nominalCapacityAh = nominalCapacityAh > 0 ? nominalCapacityAh : 38.0f;

    m_mutex = xSemaphoreCreateMutex();
    if (!m_mutex) {
        LOG_E(TAG, "Failed to create OBD telemetry mutex!");
        return false;
    }

    LOG_I(TAG, "Starting Bluetooth OBD2 service (Target: '%s', PIN: '%s', Nominal: %.1f Ah)",
          m_deviceName.c_str(), m_pin.c_str(), m_nominalCapacityAh);

    // Create background FreeRTOS task pinned to Core 0 (leaving Core 1 for WiFi/TCP)
    BaseType_t res = xTaskCreatePinnedToCore(
        taskEntry,
        "ObdTask",
        8192,
        this,
        1,
        &m_taskHandle,
        0
    );

    if (res != pdPASS) {
        LOG_E(TAG, "Failed to create OBD FreeRTOS background task!");
        return false;
    }

    return true;
#else
    LOG_I(TAG, "OBD Bluetooth telemetry is disabled in config");
    return true;
#endif
}

void ObdReader::loop() {
    // Background FreeRTOS task manages polling independently
}

bool ObdReader::isConnected() {
#if OBD_BLUETOOTH_ENABLED
    if (!m_mutex) return false;
    bool conn = false;
    if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        conn = m_telemetry.connected;
        xSemaphoreGive(m_mutex);
    }
    return conn;
#else
    return false;
#endif
}

bool ObdReader::isEcuResponding() {
#if OBD_BLUETOOTH_ENABLED
    if (!m_mutex) return false;
    bool responding = false;
    if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        responding = m_telemetry.ecuResponding;
        xSemaphoreGive(m_mutex);
    }
    return responding;
#else
    return false;
#endif
}

ObdTelemetry ObdReader::getTelemetry() {
    ObdTelemetry copy;
#if OBD_BLUETOOTH_ENABLED
    if (m_mutex && xSemaphoreTake(m_mutex, pdMS_TO_TICKS(100)) == pdTRUE) {
        copy = m_telemetry;
        xSemaphoreGive(m_mutex);
    }
#endif
    return copy;
}

#if OBD_BLUETOOTH_ENABLED

void ObdReader::taskEntry(void* param) {
    ObdReader* self = static_cast<ObdReader*>(param);
    self->runTask();
}

void ObdReader::runTask() {
    LOG_I(TAG, "OBD Task started on core %d", xPortGetCoreID());

    // Initialize BluetoothSerial in Master mode
    if (!m_serialBt.begin("ESP32_PHEV_Bridge", true)) {
        LOG_E(TAG, "Failed to initialize ESP32 Bluetooth Master mode!");
        vTaskDelete(NULL);
        return;
    }

    m_serialBt.setPin(m_pin.c_str());

    while (true) {
        // Step 1: Connect to Bluetooth ELM327 adapter
        if (!connectToAdapter()) {
            LOG_W(TAG, "OBD adapter not found. Retrying in 10s...");
            vTaskDelay(pdMS_TO_TICKS(10000));
            continue;
        }

        // Step 2: Initialize ELM327 AT commands
        if (!initializeElm()) {
            LOG_W(TAG, "ELM327 initialization failed. Reconnecting...");
            m_serialBt.disconnect();
            vTaskDelay(pdMS_TO_TICKS(5000));
            continue;
        }

        LOG_I(TAG, "ELM327 initialized! Starting Outlander PHEV diagnostic cycle.");

        // Step 3: Diagnostic polling loop
        while (m_serialBt.connected()) {
            queryBmuStatus();
            vTaskDelay(pdMS_TO_TICKS(200));

            // Only query deeper PIDs if ECU responded (i.e. car is awake/charging/driving)
            bool awake = false;
            if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
                awake = m_telemetry.ecuResponding;
                xSemaphoreGive(m_mutex);
            }

            if (awake) {
                queryCellVoltages();
                vTaskDelay(pdMS_TO_TICKS(200));

                queryBatteryTemps();
                vTaskDelay(pdMS_TO_TICKS(200));

                queryEvEcu();
                vTaskDelay(pdMS_TO_TICKS(200));

                queryInverters();

                // Normal active poll interval
                vTaskDelay(pdMS_TO_TICKS(OBD_POLL_INTERVAL_SEC * 1000));
            } else {
                // Car is asleep/parked: slow check to avoid hammering CAN bus
                vTaskDelay(pdMS_TO_TICKS(15000));
            }
        }

        LOG_W(TAG, "Bluetooth connection lost. Reconnecting...");
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(100)) == pdTRUE) {
            m_telemetry.connected = false;
            m_telemetry.ecuResponding = false;
            xSemaphoreGive(m_mutex);
        }
        vTaskDelay(pdMS_TO_TICKS(5000));
    }
}

bool ObdReader::connectToAdapter() {
    LOG_I(TAG, "Connecting to OBD2 adapter ('%s')...", m_deviceName.c_str());

    // Update connection state
    if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        m_telemetry.connected = false;
        m_telemetry.ecuResponding = false;
        xSemaphoreGive(m_mutex);
    }

    bool success = false;
    if (m_macStr.length() == 17) {
        uint8_t mac[6];
        int vals[6];
        if (sscanf(m_macStr.c_str(), "%x:%x:%x:%x:%x:%x",
                   &vals[0], &vals[1], &vals[2], &vals[3], &vals[4], &vals[5]) == 6) {
            for (int i = 0; i < 6; i++) mac[i] = (uint8_t)vals[i];
            LOG_I(TAG, "Connecting via MAC address: %s", m_macStr.c_str());
            success = m_serialBt.connect(mac);
        }
    }

    if (!success) {
        success = m_serialBt.connect(m_deviceName.c_str());
    }

    if (success) {
        LOG_I(TAG, "Bluetooth RFCOMM link established to ELM327!");
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.connected = true;
            xSemaphoreGive(m_mutex);
        }
    }
    return success;
}

String ObdReader::sendCommand(const String& cmd, uint32_t timeoutMs) {
    if (!m_serialBt.connected()) return "";

    // Clear incoming buffer
    while (m_serialBt.available()) {
        m_serialBt.read();
    }

    m_serialBt.print(cmd);
    m_serialBt.print("\r");

    String response = "";
    uint32_t start = millis();

    while (millis() - start < timeoutMs) {
        while (m_serialBt.available()) {
            char c = (char)m_serialBt.read();
            if (c == '>') { // ELM327 prompt indicates command completion
                return response;
            }
            if (c != '\r') {
                response += c;
            }
        }
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    return response;
}

bool ObdReader::initializeElm() {
    sendCommand("ATZ", 2000);   // Reset
    vTaskDelay(pdMS_TO_TICKS(300));
    sendCommand("ATE0", 500);    // Echo off
    sendCommand("ATL0", 500);    // Linefeeds off
    sendCommand("ATS0", 500);    // Spaces off
    sendCommand("ATH1", 500);    // Headers on (0x762, 0x754 visible)
    String proto = sendCommand("ATSP6", 500); // ISO 15765-4 (CAN 11-bit, 500k)

    if (proto.indexOf("OK") >= 0 || proto.length() == 0) {
        return true;
    }
    return true; // Many adapters just respond with '>'
}

bool ObdReader::parseHexBytes(const String& response, uint8_t* outBuf, size_t maxLen, size_t& outLen) {
    outLen = 0;
    String clean = "";
    for (size_t i = 0; i < response.length(); i++) {
        char c = response[i];
        if ((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')) {
            clean += c;
        }
    }

    if (clean.length() < 2) return false;

    for (size_t i = 0; i + 1 < clean.length() && outLen < maxLen; i += 2) {
        char hex[3] = { clean[i], clean[i + 1], '\0' };
        outBuf[outLen++] = (uint8_t)strtol(hex, nullptr, 16);
    }
    return outLen > 0;
}

// ------------------------------------------------------------------------------
// 1. BMU Status, Capacity, SOH, High Voltage & Current (CAN: 0x761 -> 0x762)
// ------------------------------------------------------------------------------
void ObdReader::queryBmuStatus() {
    sendCommand("ATSH 761", 500); // Target: BMU ECU
    String resp = sendCommand("2101", 1500);

    if (resp.indexOf("NO DATA") >= 0 || resp.indexOf("CAN ERROR") >= 0 || resp.indexOf("?") >= 0 || resp.length() < 10) {
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.ecuResponding = false;
            xSemaphoreGive(m_mutex);
        }
        return;
    }

    uint8_t buf[64];
    size_t len = 0;
    if (!parseHexBytes(resp, buf, sizeof(buf), len)) return;

    // Look for response header 762 followed by 61 01
    int offset = -1;
    for (size_t i = 0; i + 2 < len; i++) {
        if (buf[i] == 0x61 && buf[i + 1] == 0x01) {
            offset = i + 2;
            break;
        }
    }

    if (offset < 0 || (size_t)(offset + 25) > len) return;

    const uint8_t* d = &buf[offset];

    // High Voltage Pack Voltage: (D0 * 256 + D1) * 0.1 V
    float packVolts = ((d[0] << 8) | d[1]) * 0.1f;

    // Pack Current: Signed 16-bit, offset 32768 or two's complement * 0.1 A
    int16_t rawAmps = (int16_t)((d[2] << 8) | d[3]);
    float packAmps = rawAmps * 0.1f;
    float packKw = (packVolts * packAmps) / 1000.0f;

    // Battery Capacity (Ah) in BMU: typically bytes 23, 24
    float capacityAh = ((d[23] << 8) | d[24]) * 0.1f;
    if (capacityAh <= 0.0f || capacityAh > 60.0f) {
        // Fallback offset for certain model years
        capacityAh = ((d[21] << 8) | d[22]) * 0.1f;
    }

    float remainingAh = ((d[19] << 8) | d[20]) * 0.1f;

    float soh = 0.0f;
    if (capacityAh > 10.0f && m_nominalCapacityAh > 0.0f) {
        soh = (capacityAh / m_nominalCapacityAh) * 100.0f;
        if (soh > 105.0f) soh = 100.0f;
    }

    if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        m_telemetry.ecuResponding = true;
        m_telemetry.lastUpdate = millis();
        m_telemetry.packVoltage = packVolts;
        m_telemetry.packCurrent = packAmps;
        m_telemetry.packPowerKw = packKw;
        if (capacityAh > 5.0f) m_telemetry.capacityAh = capacityAh;
        if (remainingAh > 0.0f) m_telemetry.remainingAh = remainingAh;
        if (soh > 0.0f) m_telemetry.soh = soh;
        xSemaphoreGive(m_mutex);
    }
}

// ------------------------------------------------------------------------------
// 2. Cell Voltages (80 individual cells) (CAN: 0x761 -> 0x762, PID: 21 02)
// ------------------------------------------------------------------------------
void ObdReader::queryCellVoltages() {
    sendCommand("ATSH 761", 500);
    String resp = sendCommand("2102", 2000);

    uint8_t buf[128];
    size_t len = 0;
    if (!parseHexBytes(resp, buf, sizeof(buf), len)) return;

    int offset = -1;
    for (size_t i = 0; i + 2 < len; i++) {
        if (buf[i] == 0x61 && buf[i + 1] == 0x02) {
            offset = i + 2;
            break;
        }
    }
    if (offset < 0) return;

    // In Outlander BMU, cell voltages are encoded as 2000 mV + (byte * 10 mV) or 16-bit mV
    uint16_t minMv = 5000;
    uint16_t maxMv = 0;
    uint32_t sumMv = 0;
    uint8_t count = 0;

    for (size_t i = offset; i < len && count < 80; i++) {
        uint16_t v = 2000 + (buf[i] * 10);
        if (v >= 2500 && v <= 4300) {
            if (v < minMv) minMv = v;
            if (v > maxMv) maxMv = v;
            sumMv += v;
            count++;
        }
    }

    if (count > 40) {
        uint16_t avgMv = sumMv / count;
        uint16_t deltaMv = (maxMv >= minMv) ? (maxMv - minMv) : 0;

        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.cellVoltageMin = minMv;
            m_telemetry.cellVoltageMax = maxMv;
            m_telemetry.cellVoltageDelta = deltaMv;
            m_telemetry.cellVoltageAvg = avgMv;
            xSemaphoreGive(m_mutex);
        }
    }
}

// ------------------------------------------------------------------------------
// 3. Battery Pack Temperatures (PID: 21 03)
// ------------------------------------------------------------------------------
void ObdReader::queryBatteryTemps() {
    sendCommand("ATSH 761", 500);
    String resp = sendCommand("2103", 1500);

    uint8_t buf[64];
    size_t len = 0;
    if (!parseHexBytes(resp, buf, sizeof(buf), len)) return;

    int offset = -1;
    for (size_t i = 0; i + 2 < len; i++) {
        if (buf[i] == 0x61 && buf[i + 1] == 0x03) {
            offset = i + 2;
            break;
        }
    }
    if (offset < 0) return;

    int8_t minT = 120;
    int8_t maxT = -50;
    int32_t sumT = 0;
    uint8_t count = 0;

    for (size_t i = offset; i < len && count < 24; i++) {
        int8_t t = (int8_t)buf[i] - 40;
        if (t >= -40 && t <= 80) {
            if (t < minT) minT = t;
            if (t > maxT) maxT = t;
            sumT += t;
            count++;
        }
    }

    if (count > 0) {
        int8_t avgT = sumT / count;
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.tempMin = minT;
            m_telemetry.tempMax = maxT;
            m_telemetry.tempAvg = avgT;
            xSemaphoreGive(m_mutex);
        }
    }
}

// ------------------------------------------------------------------------------
// 4. EV-ECU & ICE Engine Telemetry (CAN: 0x753 -> 0x754, PID: 21 01)
// ------------------------------------------------------------------------------
void ObdReader::queryEvEcu() {
    sendCommand("ATSH 753", 500);
    String resp = sendCommand("2101", 1500);

    uint8_t buf[64];
    size_t len = 0;
    if (!parseHexBytes(resp, buf, sizeof(buf), len)) return;

    int offset = -1;
    for (size_t i = 0; i + 2 < len; i++) {
        if (buf[i] == 0x61 && buf[i + 1] == 0x01) {
            offset = i + 2;
            break;
        }
    }
    if (offset < 0 || (size_t)(offset + 18) > len) return;

    const uint8_t* d = &buf[offset];

    // Engine Coolant Temperature: byte - 40 (°C)
    int8_t coolant = (int8_t)d[10] - 40;

    // Fuel remaining in liters: (0..45L)
    float fuel = d[14] * 0.5f;

    // Odometer: 24-bit integer (km)
    uint32_t odo = ((uint32_t)d[15] << 16) | ((uint32_t)d[16] << 8) | d[17];

    if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        if (coolant >= -30 && coolant <= 130) m_telemetry.coolantTemp = coolant;
        if (fuel >= 0.0f && fuel <= 55.0f) m_telemetry.fuelRemainingLiters = fuel;
        if (odo > 100 && odo < 1000000) m_telemetry.odometer = odo;
        xSemaphoreGive(m_mutex);
    }
}

// ------------------------------------------------------------------------------
// 5. Electric Motor Inverters (Front 0x757, Rear 0x759)
// ------------------------------------------------------------------------------
void ObdReader::queryInverters() {
    // Front Motor (0x757 -> 0x758)
    sendCommand("ATSH 757", 500);
    String respF = sendCommand("2101", 1000);
    uint8_t bufF[32];
    size_t lenF = 0;
    if (parseHexBytes(respF, bufF, sizeof(bufF), lenF) && lenF > 8) {
        int16_t rawTorque = (int16_t)((bufF[4] << 8) | bufF[5]);
        int16_t rawRpm = (int16_t)((bufF[6] << 8) | bufF[7]);
        float pKw = (rawTorque * rawRpm) / 9549.0f;
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.frontMotorKw = pKw;
            xSemaphoreGive(m_mutex);
        }
    }

    // Rear Motor (0x759 -> 0x75A)
    sendCommand("ATSH 759", 500);
    String respR = sendCommand("2101", 1000);
    uint8_t bufR[32];
    size_t lenR = 0;
    if (parseHexBytes(respR, bufR, sizeof(bufR), lenR) && lenR > 8) {
        int16_t rawTorque = (int16_t)((bufR[4] << 8) | bufR[5]);
        int16_t rawRpm = (int16_t)((bufR[6] << 8) | bufR[7]);
        float pKw = (rawTorque * rawRpm) / 9549.0f;
        if (xSemaphoreTake(m_mutex, pdMS_TO_TICKS(50)) == pdTRUE) {
            m_telemetry.rearMotorKw = pKw;
            xSemaphoreGive(m_mutex);
        }
    }
}

#endif // OBD_BLUETOOTH_ENABLED
