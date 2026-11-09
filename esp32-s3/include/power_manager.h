#ifndef POWER_MANAGER_H
#define POWER_MANAGER_H

#include <Arduino.h>
#include "phev_client.h"
#include "cellular_modem.h"

enum PowerMode {
    POWER_MODE_ACTIVE       = 0, // Normal continuous operation (charging or driven recently)
    POWER_MODE_DUTY_CYCLE   = 1, // 3-4 days inactive: sleep 30m, wake 45s
    POWER_MODE_EMERGENCY    = 2  // 12V < 11.9V: emergency sleep to protect battery
};

class PowerManager {
public:
    PowerManager();

    void begin(PhevClient* phevClient, CellularModem* modem);
    void loop();

    float read12VVoltage();
    PowerMode getPowerMode() const { return m_mode; }
    uint32_t getHoursSinceLastCharge() const;

    void enterDutyCycleSleep();
    void enterEmergencySleep();

private:
    PhevClient* m_phev;
    CellularModem* m_modem;
    PowerMode m_mode;
    float m_lastVoltage;
    uint32_t m_lastChargeTime;
    uint32_t m_lastVoltageCheck;
    uint32_t m_wakeStartTime;

    void updateInactivityTracker();
    void checkVoltageProtection();
};

#endif // POWER_MANAGER_H
