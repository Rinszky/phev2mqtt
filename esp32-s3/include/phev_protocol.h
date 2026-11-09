#ifndef PHEV_PROTOCOL_H
#define PHEV_PROTOCOL_H

#include <Arduino.h>
#include <vector>

// Command Types
enum PhevCmdType : uint8_t {
    CMD_PING_REQ       = 0xf3,
    CMD_PING_RESP      = 0x3f,
    CMD_RESP           = 0x6f, // Car -> Client
    CMD_SEND           = 0xf6, // Client -> Car
    CMD_MY14_START_REQ = 0x4e,
    CMD_MY14_START_RESP= 0xe4,
    CMD_MY18_START_REQ = 0x5e,
    CMD_MY18_START_RESP= 0xe5,
    CMD_MY24_START_REQ = 0x6e,
    CMD_MY24_START_RESP= 0xe6,
    CMD_BAD_ENCODING   = 0xbb,
    CMD_SESSION_CLOSE  = 0xcc
};

// Register IDs
enum PhevRegister : uint8_t {
    REG_PRE_AC_NOTIF       = 0x01,
    REG_BATTERY_WARNING    = 0x02,
    REG_ECU_CAPS           = 0x03,
    REG_CHARGE_TIMERS      = 0x04,
    REG_CLIMATE_TIMERS     = 0x05,
    REG_ALT_VIN            = 0x06,
    REG_WIFI_CHANNEL       = 0x07,
    REG_WIFI_PASS_RESULT   = 0x08,
    REG_TIMER_WRITE_RESULT = 0x09,
    REG_PRE_AC_RESULT      = 0x0a,
    REG_WIFI_COMM_MODE     = 0x0b,
    REG_QUICK_CHARGER_SUB  = 0x0c,
    REG_CHARGE_INHIBIT     = 0x0d,
    REG_CUSTOMIZATION_READ = 0x0e,
    REG_CUSTOMIZATION_RES  = 0x0f,
    REG_PRE_AC_STATE       = 0x10,
    REG_SSID_RESULT        = 0x11,
    REG_VEHICLE_RTC        = 0x12,
    REG_COMM_START_NOTIF   = 0x13,
    REG_ALARM_EVENT        = 0x14,
    REG_VIN_REGISTERED     = 0x15,
    REG_CUSTOM_SETTINGS    = 0x16,
    REG_CHARGE_OVERRIDE    = 0x17,
    REG_V2H_POWER          = 0x18,
    REG_MY14_CHARGE_T3     = 0x19,
    REG_IGNITION_AC_OPER   = 0x1a,
    REG_CHARGE_MASTER      = 0x1b,
    REG_AC_MODE_DUR        = 0x1c,
    REG_BATTERY_LEVEL      = 0x1d,
    REG_CHARGE_PLUG_STATUS = 0x1e,
    REG_CHARGE_STATUS_TIME = 0x1f,
    REG_MY14_CLIMATE_TIMERS= 0x20,
    REG_REMOTE_LIGHT_CTRL  = 0x21,
    REG_MY14_CLIMATE_PRESETS= 0x22,
    REG_LIGHTS_ALARM_ARMED = 0x23,
    REG_DOORS_LOCKS_HEAD   = 0x24,
    REG_TYRE_AUX_STATUS    = 0x25,
    REG_THEFT_EQUIP        = 0x26,
    REG_WIFI_ALWAYS_COMM   = 0x27,
    REG_WIFI_SSID          = 0x28,
    REG_VEHICLE_VARIANT    = 0x29,
    REG_CLIENT_REG_STATE   = 0x2a,
    REG_SHORT_ECU_VERSION  = 0x2b,
    REG_UNREG_STATUS       = 0x2c,
    REG_ECU_BEACON         = 0xaa,
    REG_FULL_ECU_VERSION   = 0xc0
};

// Raw Framing Message
struct PhevMessage {
    uint8_t type;
    uint8_t length;
    uint8_t ack;
    uint8_t reg;
    std::vector<uint8_t> data;
    uint8_t checksum;

    bool parse(const uint8_t* buf, size_t len);
    std::vector<uint8_t> serialize() const;
    static uint8_t calcChecksum(const uint8_t* buf, size_t len);
};

// Battery Level State (0x1d)
struct BatteryStatus {
    int rawLevel = 0;
    int scaledLevel = 0;
    bool valid = false;
    uint8_t subStatus = 0;
    bool parkingLights = false;
    bool lightControlDisabled = false;
};

// Doors, Locks & Headlights (0x24)
struct VehicleDoors {
    bool locked = false;
    bool driverOpen = false;
    bool passengerOpen = false;
    bool rearRightOpen = false;
    bool rearLeftOpen = false;
    bool bootOpen = false;
    bool bonnetOpen = false;
    bool headlights = false;
};

// Climate Operating Status (0x10 & 0x1a & 0x1c)
enum PreACMode : uint8_t {
    PRE_AC_OFF        = 0,
    PRE_AC_COOL       = 1,
    PRE_AC_HEAT       = 2,
    PRE_AC_WINDSCREEN = 3
};

struct ClimateStatus {
    uint8_t state = 0; // 0=off, 1=starting/armed, 2=on/active, 3=stopping
    bool operating = false;
    bool delayPending = false;
    uint8_t mode = PRE_AC_OFF;
    uint8_t selectedDuration = 10; // 10, 20, 30 min
    uint8_t selectedDelay = 0;    // 0, 5, 10 min
    bool hasStartTime = false;
    int startHour = 0;
    int startMinute = 0;
    int remainingMinutes = 0;
    String stopTime = "--:--";
    String completionTime = "none";
};

// Charging Status (0x1e & 0x1f)
struct ChargingStatus {
    bool plugged = false;
    bool ready = false;
    bool charging = false;
    bool quickCharging = false;
    uint16_t remainingMinutes = 0;
    String completionTime = "none";
};

// Helper: Linear scaling for battery SoC (tops out at maxSoc, e.g. 94%, scaled to 100%)
int scaleBatteryLevel(int rawLevel, int maxSoc);

#endif // PHEV_PROTOCOL_H
