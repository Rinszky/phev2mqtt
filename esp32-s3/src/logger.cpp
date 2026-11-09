#include "logger.h"
#include <freertos/FreeRTOS.h>
#include <freertos/semphr.h>

static char* s_logRingBuffer = nullptr;
static size_t s_bufferCapacity = 0;
static size_t s_writeIndex = 0;
static bool s_bufferWrapped = false;
static SemaphoreHandle_t s_logMutex = nullptr;
static LogLevel s_minLevel = LOG_LEVEL_DEBUG;

void Logger::init(size_t bufferSize) {
    if (!s_logMutex) {
        s_logMutex = xSemaphoreCreateMutex();
    }
    s_bufferCapacity = bufferSize;
    if (s_logRingBuffer) {
        free(s_logRingBuffer);
    }
    s_logRingBuffer = (char*)malloc(bufferSize);
    s_writeIndex = 0;
    s_bufferWrapped = false;
}

void Logger::setLogLevel(LogLevel level) {
    s_minLevel = level;
}

void Logger::log(LogLevel level, const char* tag, const char* format, ...) {
    if (level < s_minLevel) return;

    char lineBuffer[256];
    const char* lvlStr = "I";
    switch (level) {
        case LOG_LEVEL_DEBUG: lvlStr = "D"; break;
        case LOG_LEVEL_INFO:  lvlStr = "I"; break;
        case LOG_LEVEL_WARN:  lvlStr = "W"; break;
        case LOG_LEVEL_ERROR: lvlStr = "E"; break;
    }

    uint32_t ms = millis();
    int headerLen = snprintf(lineBuffer, sizeof(lineBuffer), "[%08u][%s][%s] ", ms, lvlStr, tag);

    va_list args;
    va_start(args, format);
    int bodyLen = vsnprintf(lineBuffer + headerLen, sizeof(lineBuffer) - headerLen - 2, format, args);
    va_end(args);

    if (bodyLen < 0) bodyLen = 0;
    size_t totalLen = headerLen + bodyLen;
    lineBuffer[totalLen] = '\n';
    lineBuffer[totalLen + 1] = '\0';
    totalLen += 1;

    // Serial output
    Serial.print(lineBuffer);

    // Ring buffer output
    if (s_logRingBuffer && s_logMutex) {
        if (xSemaphoreTake(s_logMutex, pdMS_TO_TICKS(10)) == pdTRUE) {
            for (size_t i = 0; i < totalLen; i++) {
                s_logRingBuffer[s_writeIndex] = lineBuffer[i];
                s_writeIndex++;
                if (s_writeIndex >= s_bufferCapacity) {
                    s_writeIndex = 0;
                    s_bufferWrapped = true;
                }
            }
            xSemaphoreGive(s_logMutex);
        }
    }
}

void Logger::debug(const char* tag, const char* format, ...) {
    va_list args;
    va_start(args, format);
    char buf[256];
    vsnprintf(buf, sizeof(buf), format, args);
    va_end(args);
    log(LOG_LEVEL_DEBUG, tag, "%s", buf);
}

void Logger::info(const char* tag, const char* format, ...) {
    va_list args;
    va_start(args, format);
    char buf[256];
    vsnprintf(buf, sizeof(buf), format, args);
    va_end(args);
    log(LOG_LEVEL_INFO, tag, "%s", buf);
}

void Logger::warn(const char* tag, const char* format, ...) {
    va_list args;
    va_start(args, format);
    char buf[256];
    vsnprintf(buf, sizeof(buf), format, args);
    va_end(args);
    log(LOG_LEVEL_WARN, tag, "%s", buf);
}

void Logger::error(const char* tag, const char* format, ...) {
    va_list args;
    va_start(args, format);
    char buf[256];
    vsnprintf(buf, sizeof(buf), format, args);
    va_end(args);
    log(LOG_LEVEL_ERROR, tag, "%s", buf);
}

void Logger::getLogs(String& output, size_t maxBytes) {
    if (!s_logRingBuffer || !s_logMutex) return;
    if (xSemaphoreTake(s_logMutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        if (!s_bufferWrapped) {
            size_t len = s_writeIndex > maxBytes ? maxBytes : s_writeIndex;
            output.reserve(len);
            for (size_t i = s_writeIndex - len; i < s_writeIndex; i++) {
                output += s_logRingBuffer[i];
            }
        } else {
            size_t available = s_bufferCapacity;
            size_t take = available > maxBytes ? maxBytes : available;
            output.reserve(take);
            size_t start = (s_writeIndex + s_bufferCapacity - take) % s_bufferCapacity;
            for (size_t i = 0; i < take; i++) {
                output += s_logRingBuffer[(start + i) % s_bufferCapacity];
            }
        }
        xSemaphoreGive(s_logMutex);
    }
}

void Logger::clearLogs() {
    if (s_logMutex && xSemaphoreTake(s_logMutex, pdMS_TO_TICKS(50)) == pdTRUE) {
        s_writeIndex = 0;
        s_bufferWrapped = false;
        xSemaphoreGive(s_logMutex);
    }
}
