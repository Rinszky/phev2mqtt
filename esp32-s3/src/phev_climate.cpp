#include "phev_climate.h"

ClimateTracker::ClimateTracker()
    : m_state(0),
      m_operating(false),
      m_delayPending(false),
      m_mode(PRE_AC_OFF),
      m_selectedDuration(10),
      m_selectedDelay(0),
      m_hasStartTime(false),
      m_startHour(0),
      m_startMinute(0),
      m_operatingStartTime(0),
      m_delayStartTime(0),
      m_remainingMinutes(0),
      m_startTimeStr("--:--"),
      m_stopTimeStr("--:--"),
      m_completionTimeStr("none") {
}

void ClimateTracker::setModeAndDuration(uint8_t mode, uint8_t duration) {
    m_mode = mode;
    if (duration == 10 || duration == 20 || duration == 30) {
        m_selectedDuration = duration;
    }
}

void ClimateTracker::setPreACState(uint8_t state, bool hasTarget, int hour, int minute) {
    m_state = state;
    m_hasStartTime = (state == 1) && hasTarget && (hour < 24) && (minute < 60);
    m_startHour = hour;
    m_startMinute = minute;

    bool isOp = m_operating || (m_state == 2); // 2 = PreACOn
    time_t now = time(nullptr);
    if (isOp && m_operatingStartTime == 0) {
        m_operatingStartTime = now;
    } else if (!isOp && !m_delayPending) {
        m_operatingStartTime = 0;
        m_delayStartTime = 0;
    }
    updateTiming(now);
}

void ClimateTracker::setACOperStatus(bool operating, uint8_t duration, uint8_t delay, bool delayPending) {
    m_operating = operating;
    time_t now = time(nullptr);
    if (delayPending && !m_delayPending) {
        m_delayStartTime = now;
    } else if (!delayPending) {
        m_delayStartTime = 0;
    }
    m_delayPending = delayPending;

    bool isOp = operating || (m_state == 2);
    if (isOp && m_operatingStartTime == 0) {
        m_operatingStartTime = now;
    } else if (!isOp && !m_delayPending) {
        m_operatingStartTime = 0;
    }

    if (duration == 10 || duration == 20 || duration == 30) {
        m_selectedDuration = duration;
    }
    if (delay == 0 || delay == 5 || delay == 10) {
        m_selectedDelay = delay;
    }
    updateTiming(now);
}

void ClimateTracker::setScheduledTimer(int index, bool enabled, int hour, int minute, uint8_t duration, uint8_t days) {
    if (index >= 0 && index < 5) {
        m_timers[index].enabled = enabled;
        m_timers[index].hour = hour;
        m_timers[index].minute = minute;
        m_timers[index].duration = duration;
        m_timers[index].days = days;
    }
}

int ClimateTracker::findScheduledDuration(int hour, int minute) const {
    for (int i = 0; i < 5; i++) {
        if (m_timers[i].enabled && m_timers[i].hour == hour && m_timers[i].minute == minute) {
            return (int)m_timers[i].duration;
        }
    }
    return 0;
}

int ClimateTracker::activeDuration() const {
    if (m_hasStartTime && m_state == 1) {
        int schedDur = findScheduledDuration(m_startHour, m_startMinute);
        if (schedDur > 0) return schedDur;
    }
    return (m_selectedDuration > 0) ? (int)m_selectedDuration : 10;
}

void ClimateTracker::updateTiming(time_t nowTime) {
    bool isOp = m_operating || (m_state == 2);
    bool isArmedStandby = (m_state == 1) && !isOp && !m_delayPending; // PreACStarting waiting for scheduled time

    if (isArmedStandby || (!isOp && !m_delayPending)) {
        m_remainingMinutes = 0;
        m_startTimeStr = "--:--";
        m_stopTimeStr = "--:--";
        m_completionTimeStr = "none";
        return;
    }

    int dur = activeDuration();

    // 1. Scheduled HVAC active with known start time
    if (isOp && m_hasStartTime) {
        char buf[8];
        snprintf(buf, sizeof(buf), "%02d:%02d", m_startHour, m_startMinute);
        m_startTimeStr = buf;

        int stopTotalMin = m_startHour * 60 + m_startMinute + dur;
        int stopHour = (stopTotalMin / 60) % 24;
        int stopMin = stopTotalMin % 60;
        snprintf(buf, sizeof(buf), "%02d:%02d", stopHour, stopMin);
        m_stopTimeStr = buf;

        struct tm timeinfo;
        if (localtime_r(&nowTime, &timeinfo) != nullptr) {
            struct tm stopTm = timeinfo;
            stopTm.tm_sec = 0;
            stopTm.tm_min = stopMin;
            stopTm.tm_hour = stopHour;
            time_t stopEpoch = mktime(&stopTm);

            if (stopEpoch < nowTime && (nowTime - stopEpoch) > 12 * 3600) {
                stopEpoch += 24 * 3600; // Next day
            }
            double diffSec = difftime(stopEpoch, nowTime);
            int rem = (int)((diffSec + 59.0) / 60.0);
            if (rem < 0) rem = 0;
            if (rem > dur) rem = dur;
            m_remainingMinutes = rem;

            char isoBuf[32];
            strftime(isoBuf, sizeof(isoBuf), "%Y-%m-%dT%H:%M:%SZ", gmtime(&stopEpoch));
            m_completionTimeStr = isoBuf;
        }
        return;
    }

    // 2. Delayed start pending
    if (m_delayPending && !isOp) {
        int totalDur = (int)m_selectedDelay + (int)m_selectedDuration;
        time_t refStart = (m_delayStartTime > 0) ? m_delayStartTime : nowTime;
        time_t compEpoch = refStart + totalDur * 60;

        double diffSec = difftime(compEpoch, nowTime);
        int rem = (int)((diffSec + 59.0) / 60.0);
        if (rem < 0) rem = 0;
        if (rem > totalDur) rem = totalDur;
        m_remainingMinutes = rem;

        char buf[8];
        struct tm tmStart, tmComp;
        localtime_r(&refStart, &tmStart);
        localtime_r(&compEpoch, &tmComp);
        snprintf(buf, sizeof(buf), "%02d:%02d", tmStart.tm_hour, tmStart.tm_min);
        m_startTimeStr = buf;
        snprintf(buf, sizeof(buf), "%02d:%02d", tmComp.tm_hour, tmComp.tm_min);
        m_stopTimeStr = buf;

        char isoBuf[32];
        strftime(isoBuf, sizeof(isoBuf), "%Y-%m-%dT%H:%M:%SZ", gmtime(&compEpoch));
        m_completionTimeStr = isoBuf;
        return;
    }

    // 3. Manual Immediate HVAC running
    if (isOp) {
        time_t refStart = (m_operatingStartTime > 0) ? m_operatingStartTime : nowTime;
        time_t compEpoch = refStart + dur * 60;

        double diffSec = difftime(compEpoch, nowTime);
        int rem = (int)((diffSec + 59.0) / 60.0);
        if (rem < 0) rem = 0;
        if (rem > dur) rem = dur;
        m_remainingMinutes = rem;

        char buf[8];
        struct tm tmStart, tmComp;
        localtime_r(&refStart, &tmStart);
        localtime_r(&compEpoch, &tmComp);
        snprintf(buf, sizeof(buf), "%02d:%02d", tmStart.tm_hour, tmStart.tm_min);
        m_startTimeStr = buf;
        snprintf(buf, sizeof(buf), "%02d:%02d", tmComp.tm_hour, tmComp.tm_min);
        m_stopTimeStr = buf;

        char isoBuf[32];
        strftime(isoBuf, sizeof(isoBuf), "%Y-%m-%dT%H:%M:%SZ", gmtime(&compEpoch));
        m_completionTimeStr = isoBuf;
        return;
    }
}

bool ClimateTracker::isOperating() const {
    return m_operating || (m_state == 2);
}

bool ClimateTracker::isHeating() const {
    return isOperating() && (m_mode == PRE_AC_HEAT || m_mode == PRE_AC_OFF);
}

bool ClimateTracker::isCooling() const {
    return isOperating() && (m_mode == PRE_AC_COOL);
}

bool ClimateTracker::isWindscreen() const {
    return isOperating() && (m_mode == PRE_AC_WINDSCREEN);
}

String ClimateTracker::getModeString() const {
    if (!isOperating()) return "off";
    switch (m_mode) {
        case PRE_AC_COOL: return "cool";
        case PRE_AC_HEAT: return "heat";
        case PRE_AC_WINDSCREEN: return "windscreen";
        default: return "heat";
    }
}

String ClimateTracker::getHVACAction() const {
    if (!isOperating()) return "off";
    switch (m_mode) {
        case PRE_AC_COOL: return "cooling";
        case PRE_AC_HEAT: return "heating";
        case PRE_AC_WINDSCREEN: return "defrosting";
        default: return "heating";
    }
}
