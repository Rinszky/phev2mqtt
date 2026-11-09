#include "cellular_modem.h"
#include "config.h"
#include "logger.h"

#ifndef TINY_GSM_MODEM_SIM7600
#define TINY_GSM_MODEM_SIM7600
#endif
#include <TinyGsmClient.h>

#define TAG "LTE"

static TinyGsm* s_pModem = nullptr;
static TinyGsmClient* s_pGsmClient = nullptr;

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
    LOG_I(TAG, "Connecting to LTE APN: %s", m_apn.c_str());
    if (!s_pModem->gprsConnect(m_apn.c_str(), m_user.c_str(), m_pass.c_str())) {
        LOG_E(TAG, "Failed to connect to APN!");
        m_connected = false;
        return false;
    }
    m_connected = true;
    String localIP = s_pModem->getLocalIP();
    LOG_I(TAG, "LTE Connected! Cellular IP: %s", localIP.c_str());
    return true;
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
