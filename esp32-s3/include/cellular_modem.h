#ifndef CELLULAR_MODEM_H
#define CELLULAR_MODEM_H

#include <Arduino.h>
#include <HardwareSerial.h>
#include <Client.h>

class CellularModem {
public:
    CellularModem();

    bool begin(const char* apn, const char* user = "", const char* pass = "");
    void loop();

    bool isConnected() const { return m_connected; }
    int getSignalQuality() const { return m_signalQuality; }
    String getOperator() const { return m_operator; }
    String getModemInfo() const { return m_modemInfo; }
    String getIMEI() const { return m_imei; }

    void powerOn();
    void powerOff();
    void enterSleep();
    void wakeFromSleep();

    Client* getNetworkClient();

private:
    HardwareSerial m_serial;
    String m_apn;
    String m_user;
    String m_pass;
    bool m_connected;
    bool m_powered;
    bool m_sleeping;
    int m_signalQuality; // 0..31 CSQ
    String m_operator;
    String m_modemInfo;
    String m_imei;
    uint32_t m_lastConnCheck;

    bool sendATCommand(const char* cmd, const char* expected, uint32_t timeoutMs = 2000, String* response = nullptr);
    bool initModem();
    bool connectGPRS();
};

#endif // CELLULAR_MODEM_H
