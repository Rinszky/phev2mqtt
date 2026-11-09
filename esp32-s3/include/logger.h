#ifndef PHEV_LOGGER_H
#define PHEV_LOGGER_H

#include <Arduino.h>
#include <stdarg.h>

enum LogLevel {
    LOG_LEVEL_DEBUG = 0,
    LOG_LEVEL_INFO  = 1,
    LOG_LEVEL_WARN  = 2,
    LOG_LEVEL_ERROR = 3
};

class Logger {
public:
    static void init(size_t bufferSize = 8192);
    static void log(LogLevel level, const char* tag, const char* format, ...);
    static void getLogs(String& output, size_t maxBytes = 4096);
    static void clearLogs();
    static void setLogLevel(LogLevel level);

    // Convenience helpers
    static void debug(const char* tag, const char* format, ...);
    static void info(const char* tag, const char* format, ...);
    static void warn(const char* tag, const char* format, ...);
    static void error(const char* tag, const char* format, ...);
};

#define LOG_D(tag, ...) Logger::debug(tag, __VA_ARGS__)
#define LOG_I(tag, ...) Logger::info(tag, __VA_ARGS__)
#define LOG_W(tag, ...) Logger::warn(tag, __VA_ARGS__)
#define LOG_E(tag, ...) Logger::error(tag, __VA_ARGS__)

#endif // PHEV_LOGGER_H
