#ifndef PHEV_CLIMATE_H
#define PHEV_CLIMATE_H

#include <Arduino.h>
#include "phev_protocol.h"
#include <time.h>

class ClimateTracker {
public:
    ClimateTracker();

    void setModeAndDuration(uint8_t mode, uint8_t duration);
    void setPreACState(uint8_t state, bool hasTarget, int hour, int minute);
    void setACOperStatus(bool operating, uint8_t duration, uint8_t delay, bool delayPending);
    void setScheduledTimer(int index, bool enabled, int hour, int minute, uint8_t duration, uint8_t days);

    void updateTiming(time_t nowTime);
    
    bool isHeating() const;
    bool isCooling() const;
    bool isWindscreen() const;
    bool isOperating() const;
    String getModeString() const;
    String getHVACAction() const;

    int getRemainingMinutes() const { return m_remainingMinutes; }
    String getStartTime() const { return m_startTimeStr; }
    String getStopTime() const { return m_stopTimeStr; }
    String getCompletionTime() const { return m_completionTimeStr; }

    uint8_t getSelectedDuration() const { return m_selectedDuration; }
    uint8_t getSelectedDelay() const { return m_selectedDelay; }

private:
    uint8_t m_state;           // 0=off, 1=starting, 2=on, 3=terminated
    bool m_operating;
    bool m_delayPending;
    uint8_t m_mode;
    uint8_t m_selectedDuration;
    uint8_t m_selectedDelay;

    bool m_hasStartTime;
    int m_startHour;
    int m_startMinute;

    time_t m_operatingStartTime;
    time_t m_delayStartTime;

    int m_remainingMinutes;
    String m_startTimeStr;
    String m_stopTimeStr;
    String m_completionTimeStr;

    struct ScheduledTimer {
        bool enabled = false;
        int hour = 0;
        int minute = 0;
        uint8_t duration = 10;
        uint8_t days = 0;
    } m_timers[5];

    int findScheduledDuration(int hour, int minute) const;
    int activeDuration() const;
};

#endif // PHEV_CLIMATE_H
