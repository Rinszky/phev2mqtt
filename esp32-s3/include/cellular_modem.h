#ifndef CELLULAR_MODEM_H
#define CELLULAR_MODEM_H

#include <Arduino.h>
#include <HardwareSerial.h>
#include <Client.h>

class CellularModem {
public:
    CellularModem();

    bool begin(const char* apn, const char* user = "", const char* pass = "", const char* pin = "");
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
    Client* getSecureNetworkClient();

    // GPS / GNSS Tracking
    bool enableGPS();
    bool disableGPS();
    bool pollGPS();
    bool isGPSFix() const { return m_gpsFix; }
    float getLatitude() const { return m_latitude; }
    float getLongitude() const { return m_longitude; }
    float getSpeed() const { return m_speed; }
    float getAltitude() const { return m_altitude; }
    int getSatellites() const { return m_satellites; }

private:
    HardwareSerial m_serial;
    String m_apn;
    String m_user;
    String m_pass;
    String m_pin;
    bool m_connected;
    bool m_powered;
    bool m_sleeping;
    int m_signalQuality; // 0..31 CSQ
    String m_operator;
    String m_modemInfo;
    String m_imei;
    uint32_t m_lastConnCheck;

    // GPS State
    bool m_gpsEnabled;
    bool m_gpsFix;
    float m_latitude;
    float m_longitude;
    float m_speed;
    float m_altitude;
    int m_satellites;
    uint32_t m_lastGpsPoll;

    bool initModem();
    void synchronizeClock();
    bool connectGPRS();
};

#endif // CELLULAR_MODEM_H
