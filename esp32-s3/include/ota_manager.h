#ifndef OTA_MANAGER_H
#define OTA_MANAGER_H

#include <Arduino.h>

class OTAManager {
public:
    static bool performOTA(const char* binaryUrl);
};

#endif // OTA_MANAGER_H
