#include "cellular_modem.h"
#include "config.h"
#include "logger.h"

#define TINY_GSM_MODEM_SIM7600
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
      m_lastConnCheck(0) {
}

bool CellularModem::begin(const char* apn, const char* user, const char* pass) {
    m_apn = apn;
    m_user = user;
    m_pass = pass;

    pinMode(MODEM_PWRKEY_PIN, OUTPUT);
    pinMode(MODEM_RESET_PIN, OUTPUT);
    pinMode(MODEM_DTR_PIN, OUTPUT);
    pinMode(MODEM_STATUS_PIN, INPUT);

    digitalWrite(MODEM_DTR_PIN, LOW); // Awake
    digitalWrite(MODEM_RESET_PIN, LOW);

    LOG_I(TAG, "Initializing LTE UART on TX=%d RX=%d at %d baud", MODEM_TX_PIN, MODEM_RX_PIN, MODEM_BAUDRATE);
    m_serial.begin(MODEM_BAUDRATE, SERIAL_8N1, MODEM_RX_PIN, MODEM_TX_PIN);

    powerOn();
    return initModem();
}

void CellularModem::powerOn() {
    LOG_I(TAG, "Pulsing LTE Modem PWRKEY to power on...");
    digitalWrite(MODEM_PWRKEY_PIN, LOW);
    delay(100);
    digitalWrite(MODEM_PWRKEY_PIN, HIGH);
    delay(1000); // 1s pulse for SIM7600/SIM7000
    digitalWrite(MODEM_PWRKEY_PIN, LOW);
    delay(3000); // Wait for boot
    m_powered = true;
    m_sleeping = false;
}

void CellularModem::powerOff() {
    LOG_I(TAG, "Powering down LTE Modem to conserve 12V battery...");
    if (s_pModem) {
        s_pModem->poweroff();
    }
    digitalWrite(MODEM_PWRKEY_PIN, HIGH);
    delay(2500);
    digitalWrite(MODEM_PWRKEY_PIN, LOW);
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

bool CellularModem::initModem() {
    if (!s_pModem) {
        s_pModem = new TinyGsm(m_serial);
    }
    if (!s_pGsmClient) {
        s_pGsmClient = new TinyGsmClient(*s_pModem);
    }

    LOG_I(TAG, "Testing AT communication with modem...");
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
}

Client* CellularModem::getNetworkClient() {
    return s_pGsmClient;
}
