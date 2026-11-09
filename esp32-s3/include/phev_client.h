#ifndef PHEV_CLIENT_H
#define PHEV_CLIENT_H

#include <Arduino.h>
#include <WiFi.h>
#include <WiFiClient.h>
#include "phev_protocol.h"
#include "phev_climate.h"

typedef void (*PhevMessageCallback)(const PhevMessage& msg);

class PhevClient {
public:
    PhevClient();

    bool begin(const char* ssid, const char* password);
    void loop();

    bool isWifiConnected() const { return m_wifiConnected; }
    bool isCarConnected() const { return m_carConnected; }

    void setMessageCallback(PhevMessageCallback cb) { m_msgCallback = cb; }

    // High-level car controls
    bool sendPreACCommand(uint8_t mode, uint8_t duration, uint8_t delay);
    bool sendPreACStop();
    bool sendDoorLock(bool lock);
    bool sendHeadlights(bool on);
    bool sendParkingLights(bool on);
    bool sendHornHazard(bool horn, bool hazard);
    bool sendTimeSync(struct tm* timeinfo);
    bool sendClimateTimer(int slot, bool enabled, int hour, int minute, uint8_t duration, uint8_t days, uint8_t mode);
    bool sendChargeTimer(int slot, bool enabled, int startH, int startM, int endH, int endM, uint8_t days, bool fullCharge);

    // State getters
    const BatteryStatus& getBatteryStatus() const { return m_battery; }
    const VehicleDoors& getDoors() const { return m_doors; }
    const ClimateStatus& getClimateStatus() const { return m_climateStatus; }
    const ChargingStatus& getChargingStatus() const { return m_charging; }
    ClimateTracker& getClimateTracker() { return m_climate; }

    void setBatteryMaxSoc(int maxSoc) { m_batteryMaxSoc = maxSoc; }
    int getBatteryMaxSoc() const { return m_batteryMaxSoc; }

    String getVIN() const { return m_vin; }
    String getECUVersion() const { return m_ecuVersion; }

private:
    String m_ssid;
    String m_password;
    WiFiClient m_tcpClient;
    bool m_wifiConnected;
    bool m_carConnected;
    uint32_t m_lastWifiCheck;
    uint32_t m_lastCarConnectAttempt;
    uint32_t m_lastPingSent;
    uint32_t m_lastPingReceived;
    uint8_t m_pingSeq;

    // Rolling XOR Encryption State
    uint8_t m_keyMap[256];
    uint8_t m_rNum; // Car -> Client
    uint8_t m_sNum; // Client -> Car
    bool m_securityInit;

    // Battery & Vehicle Status
    BatteryStatus m_battery;
    VehicleDoors m_doors;
    ClimateStatus m_climateStatus;
    ChargingStatus m_charging;
    ClimateTracker m_climate;

    int m_batteryMaxSoc;
    int m_lastValidRawBattery;
    int m_batteryGlitchCount;

    String m_vin;
    String m_ecuVersion;

    PhevMessageCallback m_msgCallback;

    bool connectToCar();
    void disconnectCar();
    void handleIncomingBytes();
    void processPacket(const uint8_t* raw, size_t len);
    void handleDecodedMessage(const PhevMessage& msg);

    bool sendRawPacket(uint8_t type, uint8_t ack, uint8_t reg, const uint8_t* data, size_t dataLen);
    void initSecurityKeyMap(const uint8_t* seed, size_t len);

    // Glitch filter matching our verified Go algorithm
    bool processBatteryGlitchFilter(int rawLevel, int& outScaled, int& outRaw);
};

#endif // PHEV_CLIENT_H
