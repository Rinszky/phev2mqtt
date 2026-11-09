#ifndef OBD_READER_H
#define OBD_READER_H

#include <Arduino.h>
#include "config.h"

#if OBD_BLUETOOTH_ENABLED
#include <BluetoothSerial.h>
#endif

// ==============================================================================
// OBD2 & BMU Telemetry Structure (Mitsubishi Outlander PHEV)
// ==============================================================================
struct ObdTelemetry {
    bool connected = false;         // Bluetooth connection to ELM327 adapter established
    bool ecuResponding = false;     // Vehicle ECU/BMU is awake and answering UDS diagnostic queries
    uint32_t lastUpdate = 0;        // Timestamp (millis) of last valid diagnostic packet

    // High Voltage Traction Battery (BMU ECU: 0x761 / 0x762)
    float soh = 0.0f;               // State of Health % (e.g. 84.5%)
    float capacityAh = 0.0f;        // Current maximum battery capacity in Ah (e.g. 32.1 Ah)
    float remainingAh = 0.0f;       // Remaining battery charge in Ah
    float packVoltage = 0.0f;       // Traction battery pack voltage (V)
    float packCurrent = 0.0f;       // Traction battery pack current (A: + discharge, - charge)
    float packPowerKw = 0.0f;       // Instantaneous battery power (kW)

    // Individual Cell Voltages (80 cells across 10-12 modules)
    uint16_t cellVoltageMin = 0;    // Lowest cell voltage (mV, e.g. 3980 mV)
    uint16_t cellVoltageMax = 0;    // Highest cell voltage (mV, e.g. 4015 mV)
    uint16_t cellVoltageDelta = 0;  // Cell imbalance / deviation (mV: max - min)
    uint16_t cellVoltageAvg = 0;    // Average cell voltage (mV)

    // Battery Pack Temperatures
    int8_t tempMin = 0;             // Coldest module temp (°C)
    int8_t tempMax = 0;             // Hottest module temp (°C)
    int8_t tempAvg = 0;             // Average battery pack temp (°C)

    // Cumulative Energy Counters
    uint32_t totalAcKwh = 0;        // Total energy charged via regular AC (Type 1 / Type 2) in kWh
    uint32_t totalQcKwh = 0;        // Total energy charged via DC Quick Charge (CHAdeMO) in kWh

    // Engine & Vehicle Telemetry (EV-ECU: 0x753 / 0x754)
    float fuelRemainingLiters = 0.0f; // Petrol fuel level (Liters, 0..45L)
    int8_t coolantTemp = 0;         // Engine coolant temperature (°C)
    uint32_t odometer = 0;          // Odometer (km)

    // Electric Motors (Inverters: 0x757, 0x759)
    float frontMotorKw = 0.0f;      // Front electric motor power (kW)
    float rearMotorKw = 0.0f;       // Rear electric motor power (kW)
};

class ObdReader {
public:
    ObdReader();
    ~ObdReader();

    bool begin(const char* deviceName,
               const char* pin = "1234",
               const char* macAddress = "",
               float nominalCapacityAh = 38.0f);

    void loop();

    bool isConnected();
    bool isEcuResponding();
    ObdTelemetry getTelemetry();

private:
#if OBD_BLUETOOTH_ENABLED
    BluetoothSerial m_serialBt;
    TaskHandle_t m_taskHandle;
    SemaphoreHandle_t m_mutex;

    String m_deviceName;
    String m_pin;
    String m_macStr;
    float m_nominalCapacityAh;

    ObdTelemetry m_telemetry;

    static void taskEntry(void* param);
    void runTask();

    bool connectToAdapter();
    bool initializeElm();
    String sendCommand(const String& cmd, uint32_t timeoutMs = 1500);

    void queryBmuStatus();
    void queryCellVoltages();
    void queryBatteryTemps();
    void queryEvEcu();
    void queryInverters();

    bool parseHexBytes(const String& response, uint8_t* outBuf, size_t maxLen, size_t& outLen);
#endif
};

#endif // OBD_READER_H
