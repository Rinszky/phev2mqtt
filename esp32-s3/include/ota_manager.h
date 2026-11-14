#ifndef OTA_MANAGER_H
#define OTA_MANAGER_H

#include <Arduino.h>
#include <functional>
#include "cellular_modem.h"

typedef std::function<void(const char* state, int progress, const char* error)> OtaProgressCallback;

class OTAManager {
public:
    static bool performOTA(CellularModem* modem,
                           const char* binaryUrl,
                           const char* expectedSha256 = nullptr,
                           OtaProgressCallback progressCb = nullptr);
};

#endif // OTA_MANAGER_H
