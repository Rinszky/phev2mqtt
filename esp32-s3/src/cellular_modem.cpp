#include "cellular_modem.h"
#include "config.h"
#include "logger.h"

#ifndef TINY_GSM_MODEM_SIM7600
#define TINY_GSM_MODEM_SIM7600
#endif
#include <TinyGsmClient.h>
#include <SSLClientESP32.h>
#include <sys/time.h>
#include <time.h>
#include "isrg_root_x1.h"

#define TAG "LTE"

static TinyGsm* s_pModem = nullptr;
static TinyGsmClient* s_pGsmClient = nullptr;
static SSLClientESP32* s_pSecureClient = nullptr;

static time_t toUtcEpoch(int year, int month, int day, int hour, int minute, int second) {
    year -= month <= 2;
    const int era = (year >= 0 ? year : year - 399) / 400;
    const unsigned yearOfEra = static_cast<unsigned>(year - era * 400);
    const unsigned adjustedMonth = static_cast<unsigned>(month + (month > 2 ? -3 : 9));
    const unsigned dayOfYear = (153 * adjustedMonth + 2) / 5 + day - 1;
    const unsigned dayOfEra = yearOfEra * 365 + yearOfEra / 4 - yearOfEra / 100 + dayOfYear;
    const int64_t daysSinceEpoch = static_cast<int64_t>(era) * 146097 + dayOfEra - 719468;
    return static_cast<time_t>(daysSinceEpoch * 86400 + hour * 3600 + minute * 60 + second);
}

CellularModem::CellularModem()
    : m_serial(MODEM_UART_NUM),
      m_connected(false),
      m_powered(false),
      m_sleeping(false),
      m_signalQuality(0),
      m_lastConnCheck(0),
      m_gpsEnabled(false),
      m_gpsFix(false),
      m_latitude(0.0f),
      m_longitude(0.0f),
      m_speed(0.0f),
      m_altitude(0.0f),
      m_satellites(0),
      m_lastGpsPoll(0) {
}

bool CellularModem::begin(const char* apn, const char* user, const char* pass, const char* pin) {
    m_apn = apn;
    m_user = user;
    m_pass = pass;
    m_pin = pin ? pin : "";

    pinMode(MODEM_POWERON_PIN, OUTPUT);
    digitalWrite(MODEM_POWERON_PIN, HIGH); // Power on modem peripheral bus

    pinMode(MODEM_PWRKEY_PIN, OUTPUT);
    pinMode(MODEM_RESET_PIN, OUTPUT);
    pinMode(MODEM_DTR_PIN, OUTPUT);

    digitalWrite(MODEM_DTR_PIN, LOW); // Awake
    digitalWrite(MODEM_RESET_PIN, LOW);

    LOG_I(TAG, "Initializing LilyGO T-A7670E UART on TX=%d RX=%d at %d baud",
          MODEM_RX_PIN, MODEM_TX_PIN, MODEM_BAUDRATE);
    // LilyGO Pin 26 is ESP32 RX (from modem TX), Pin 27 is ESP32 TX (to modem RX)
    m_serial.begin(MODEM_BAUDRATE, SERIAL_8N1, MODEM_TX_PIN, MODEM_RX_PIN);

    powerOn();
    bool initOk = initModem();
    if (initOk && GPS_ENABLED) {
        enableGPS();
    }
    return initOk;
}

void CellularModem::powerOn() {
    LOG_I(TAG, "Pulsing LilyGO T-A7670E PWRKEY to power on...");
    pinMode(MODEM_POWERON_PIN, OUTPUT);
    digitalWrite(MODEM_POWERON_PIN, HIGH);

    pinMode(MODEM_PWRKEY_PIN, OUTPUT);
    digitalWrite(MODEM_PWRKEY_PIN, HIGH);
    delay(1000); // 1s pulse for A7670
    digitalWrite(MODEM_PWRKEY_PIN, LOW);
    delay(2000); // Wait for modem boot
    m_powered = true;
    m_sleeping = false;
}

void CellularModem::powerOff() {
    LOG_I(TAG, "Powering down LTE Modem...");
    if (m_gpsEnabled) {
        disableGPS();
    }
    if (s_pModem) {
        s_pModem->poweroff();
    }
    digitalWrite(MODEM_POWERON_PIN, LOW);
    m_powered = false;
    m_connected = false;
}

void CellularModem::enterSleep() {
    if (m_sleeping) return;
    LOG_I(TAG, "Putting LTE modem into sleep mode (DTR HIGH + CSCLK=1)");
    if (s_pModem) {
        s_pModem->sendAT("+CSCLK=1");
        s_pModem->waitResponse();
    }
    digitalWrite(MODEM_DTR_PIN, HIGH); // DTR high tells modem to sleep
    m_sleeping = true;
}

void CellularModem::wakeFromSleep() {
    if (!m_sleeping) return;
    LOG_I(TAG, "Waking LTE modem from sleep mode (DTR LOW)");
    digitalWrite(MODEM_DTR_PIN, LOW); // DTR low wakes modem
    delay(100);
    if (s_pModem) {
        s_pModem->sendAT("+CSCLK=0");
        s_pModem->waitResponse();
    }
    m_sleeping = false;
}

bool CellularModem::enableGPS() {
    if (!s_pModem) return false;
    LOG_I(TAG, "Enabling A7670E GNSS / GPS engine...");
    m_gpsEnabled = s_pModem->enableGPS();
    if (m_gpsEnabled) {
        LOG_I(TAG, "GPS engine enabled successfully!");
    } else {
        LOG_W(TAG, "GPS enable command returned false (check antenna / firmware)");
    }
    return m_gpsEnabled;
}

bool CellularModem::disableGPS() {
    if (!s_pModem) return false;
    LOG_I(TAG, "Disabling GPS engine...");
    s_pModem->disableGPS();
    m_gpsEnabled = false;
    m_gpsFix = false;
    return true;
}

bool CellularModem::pollGPS() {
    if (!s_pModem || !m_gpsEnabled) return false;
    float lat = 0, lon = 0, speed = 0, alt = 0;
    int vsat = 0, usat = 0;

    if (s_pModem->getGPS(&lat, &lon, &speed, &alt, &vsat, &usat)) {
        m_latitude = lat;
        m_longitude = lon;
        m_speed = speed;
        m_altitude = alt;
        m_satellites = vsat;
        m_gpsFix = true;
        LOG_D(TAG, "GPS Fix: Lat=%.6f, Lon=%.6f, Speed=%.1f km/h, Sats=%d",
              lat, lon, speed, vsat);
        return true;
    }
    m_gpsFix = false;
    return false;
}

bool CellularModem::initModem() {
    if (!s_pModem) {
        s_pModem = new TinyGsm(m_serial);
    }
    if (!s_pGsmClient) {
        s_pGsmClient = new TinyGsmClient(*s_pModem);
    }
    if (!s_pSecureClient) {
        s_pSecureClient = new SSLClientESP32(s_pGsmClient);
        s_pSecureClient->setCACert(ISRG_ROOT_X1);
    }

    LOG_I(TAG, "Testing AT communication with A7670E modem...");
    int retries = 5;
    while (retries-- > 0) {
        if (s_pModem->testAT(1000)) {
            LOG_I(TAG, "Modem replied to AT!");
            break;
        }
        delay(1000);
    }
    if (retries <= 0) {
        LOG_E(TAG, "Modem did not respond to AT commands!");
        return false;
    }

    m_modemInfo = s_pModem->getModemInfo();
    m_imei = s_pModem->getIMEI();
    LOG_I(TAG, "Modem Info: %s, IMEI: %s", m_modemInfo.c_str(), m_imei.c_str());

    if (m_pin.length() > 0) {
        LOG_I(TAG, "Unlocking SIM card with PIN...");
        s_pModem->simUnlock(m_pin.c_str());
    }

    LOG_I(TAG, "Waiting for cellular network registration...");
    if (!s_pModem->waitForNetwork(60000L)) {
        LOG_W(TAG, "Cellular network registration timed out!");
        return false;
    }
    LOG_I(TAG, "Cellular network registered successfully!");

    m_operator = s_pModem->getOperator();
    m_signalQuality = s_pModem->getSignalQuality();
    LOG_I(TAG, "Operator: %s, CSQ: %d (RSSI: %d dBm)",
          m_operator.c_str(), m_signalQuality, -113 + 2 * m_signalQuality);

    return connectGPRS();
}

bool CellularModem::connectGPRS() {
    String apn = m_apn;
    if (apn.isEmpty()) {
        String carrier = m_operator;
        carrier.toLowerCase();
        apn = (carrier.indexOf("telekom") >= 0 || carrier.indexOf("yettel") >= 0 || carrier.indexOf("telenor") >= 0)
            ? "net"
            : "internet";
        LOG_I(TAG, "Selected LTE APN '%s' for operator '%s'", apn.c_str(), m_operator.c_str());
    }
    LOG_I(TAG, "Connecting to LTE APN: %s", apn.c_str());
    if (!s_pModem->gprsConnect(apn.c_str(), m_user.c_str(), m_pass.c_str())) {
        LOG_E(TAG, "Failed to connect to APN!");
        m_connected = false;
        return false;
    }
    m_connected = true;
    String localIP = s_pModem->getLocalIP();
    LOG_I(TAG, "LTE Connected! Cellular IP: %s", localIP.c_str());
    synchronizeClock();
    return true;
}

void CellularModem::synchronizeClock() {
    // TLS certificate validity checks fail when the ESP32 clock is still at
    // its default epoch. Ask the modem to sync over its cellular data bearer.
    const time_t currentTime = time(nullptr);
    if (currentTime >= 1767225600 && currentTime < 2051222400) return; // 2026 through 2034

    LOG_I(TAG, "Synchronizing system clock from cellular network for TLS...");
    const int result = s_pModem->NTPServerSync("pool.ntp.org", 0);
    if (result != 1) {
        LOG_W(TAG, "Cellular NTP sync failed (result=%d); TLS certificate checks may fail", result);
        return;
    }

    int year = 0, month = 0, day = 0, hour = 0, minute = 0, second = 0;
    float timezone = 0;
    if (!s_pModem->getNetworkTime(&year, &month, &day, &hour, &minute, &second, &timezone)) {
        LOG_W(TAG, "Could not read synchronized modem time");
        return;
    }

    const time_t epoch = toUtcEpoch(year, month, day, hour, minute, second) -
                         static_cast<time_t>(timezone * 3600.0f);
    if (epoch <= 0) {
        LOG_W(TAG, "Modem returned an invalid date/time");
        return;
    }

    timeval now = { .tv_sec = epoch, .tv_usec = 0 };
    settimeofday(&now, nullptr);
    LOG_I(TAG, "System clock synchronized: %04d-%02d-%02d %02d:%02d:%02d UTC",
          year, month, day, hour, minute, second);
}

void CellularModem::loop() {
    if (m_sleeping) return;
    uint32_t now = millis();

    // Reconnection check
    if (now - m_lastConnCheck > 15000) {
        m_lastConnCheck = now;
        if (s_pModem) {
            if (!s_pModem->isGprsConnected()) {
                LOG_W(TAG, "LTE connection lost! Reconnecting...");
                m_connected = false;
                connectGPRS();
            } else {
                m_connected = true;
                m_signalQuality = s_pModem->getSignalQuality();
            }
        }
    }

    // Periodic GPS polling
    if (GPS_ENABLED && m_gpsEnabled && (now - m_lastGpsPoll > (GPS_POLL_INTERVAL_SEC * 1000))) {
        m_lastGpsPoll = now;
        pollGPS();
    }
}

Client* CellularModem::getNetworkClient() {
    return s_pGsmClient;
}

Client* CellularModem::getSecureNetworkClient() {
    return s_pSecureClient;
}
