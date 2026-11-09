#include "power_manager.h"
#include "config.h"
#include "logger.h"
#include <esp_sleep.h>
#include <driver/rtc_io.h>
#include <WiFi.h>

#define TAG "PWR"

// RTC Fast Memory variables that persist across deep sleeps
RTC_DATA_ATTR static uint32_t s_rtcLastChargeTime = 0;
RTC_DATA_ATTR static uint32_t s_rtcTotalSleepCycles = 0;

PowerManager::PowerManager()
    : m_phev(nullptr),
      m_modem(nullptr),
      m_mode(POWER_MODE_ACTIVE),
      m_lastVoltage(12.6f),
      m_lastChargeTime(0),
      m_lastVoltageCheck(0),
      m_wakeStartTime(0) {
}

void PowerManager::begin(PhevClient* phevClient, CellularModem* modem) {
    m_phev = phevClient;
    m_modem = modem;
    m_wakeStartTime = millis();

    analogReadResolution(12);
    analogSetAttenuation(ADC_11db); // Full range 0 - 3.1V

    if (s_rtcLastChargeTime == 0) {
        s_rtcLastChargeTime = (uint32_t)time(nullptr);
    }
    m_lastChargeTime = s_rtcLastChargeTime;

    float v = read12VVoltage();
    LOG_I(TAG, "Power Manager initialized. 12V Battery Voltage: %.2f V", v);

    esp_sleep_wakeup_cause_t cause = esp_sleep_get_wakeup_cause();
    if (cause == ESP_SLEEP_WAKEUP_TIMER) {
        s_rtcTotalSleepCycles++;
        LOG_I(TAG, "Woke from Duty-Cycle Deep Sleep (Cycle #%d)", s_rtcTotalSleepCycles);
    }
}

float PowerManager::read12VVoltage() {
    int raw = analogRead(BATTERY_12V_ADC_PIN);
    // ADC calibration: 3.3V / 4095 * divider ratio
    float vPin = (raw / 4095.0f) * 3.3f;
    float vBat = vPin * BATTERY_12V_DIVIDER_RATIO;
    m_lastVoltage = vBat;
    return vBat;
}

uint32_t PowerManager::getHoursSinceLastCharge() const {
    time_t now = time(nullptr);
    if (now > m_lastChargeTime) {
        return (uint32_t)((now - m_lastChargeTime) / 3600);
    }
    return 0;
}

void PowerManager::updateInactivityTracker() {
    if (!m_phev) return;

    const ChargingStatus& chg = m_phev->getChargingStatus();
    // If the car is actively charging, plugged in, or driving (ready)
    if (chg.charging || chg.plugged) {
        m_lastChargeTime = (uint32_t)time(nullptr);
        s_rtcLastChargeTime = m_lastChargeTime;
        m_mode = POWER_MODE_ACTIVE;
    }
}

void PowerManager::checkVoltageProtection() {
    float v = read12VVoltage();

    // 1. Emergency Under-Voltage Protection (< 11.9V)
    if (v < BATTERY_12V_LOW_CUTOFF) {
        LOG_E(TAG, "EMERGENCY: 12V Auxiliary Battery is CRITICALLY LOW: %.2f V (< %.2f V)!",
              v, BATTERY_12V_LOW_CUTOFF);
        LOG_E(TAG, "Entering emergency deep sleep to prevent 12V battery drain!");
        enterEmergencySleep();
        return;
    }

    // 2. Inactivity Check (3-4 days without charging)
    uint32_t hoursInactive = getHoursSinceLastCharge();
    if (hoursInactive >= INACTIVITY_SLEEP_HOURS) {
        m_mode = POWER_MODE_DUTY_CYCLE;
        LOG_W(TAG, "Vehicle inactive for %d hours (>= %d h)! Switching to Duty-Cycle Low Power Mode",
              hoursInactive, INACTIVITY_SLEEP_HOURS);
        
        // If awake for more than the duty cycle wake window (e.g. 45s), go back to sleep
        if (millis() - m_wakeStartTime > (DUTY_CYCLE_WAKE_SECONDS * 1000)) {
            enterDutyCycleSleep();
        }
    } else {
        m_mode = POWER_MODE_ACTIVE;
    }
}

void PowerManager::loop() {
    uint32_t now = millis();
    if (now - m_lastVoltageCheck > 10000) {
        m_lastVoltageCheck = now;
        updateInactivityTracker();
        checkVoltageProtection();
    }
}

void PowerManager::enterDutyCycleSleep() {
    LOG_I(TAG, "Shutting down peripherals for %d min Duty-Cycle Sleep...", DUTY_CYCLE_SLEEP_MINUTES);
    if (m_modem) {
        m_modem->enterSleep();
    }
    WiFi.disconnect(true);
    WiFi.mode(WIFI_OFF);

    uint64_t sleepUs = (uint64_t)DUTY_CYCLE_SLEEP_MINUTES * 60ULL * 1000000ULL;
    esp_sleep_enable_timer_wakeup(sleepUs);
    LOG_I(TAG, "Entering Deep Sleep now. Goodnight!");
    Serial.flush();
    esp_deep_sleep_start();
}

void PowerManager::enterEmergencySleep() {
    LOG_E(TAG, "Shutting down ALL peripherals for Emergency Sleep...");
    if (m_modem) {
        m_modem->powerOff();
    }
    WiFi.disconnect(true);
    WiFi.mode(WIFI_OFF);

    // Sleep for 2 hours before waking up for 5 seconds to check if battery was charged
    uint64_t sleepUs = 2ULL * 3600ULL * 1000000ULL;
    esp_sleep_enable_timer_wakeup(sleepUs);
    Serial.flush();
    esp_deep_sleep_start();
}
