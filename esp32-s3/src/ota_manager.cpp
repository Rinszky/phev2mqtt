#include "ota_manager.h"
#include "logger.h"
#include <HTTPClient.h>
#include <Update.h>

#define TAG "OTA"

bool OTAManager::performOTA(const char* binaryUrl) {
    LOG_I(TAG, "Starting OTA update from URL: %s", binaryUrl);
    HTTPClient http;
    http.begin(binaryUrl);

    int httpCode = http.GET();
    if (httpCode != HTTP_CODE_OK) {
        LOG_E(TAG, "HTTP GET failed, error: %s (code %d)", http.errorToString(httpCode).c_str(), httpCode);
        http.end();
        return false;
    }

    int contentLength = http.getSize();
    LOG_I(TAG, "Firmware binary size: %d bytes", contentLength);

    if (contentLength <= 0) {
        LOG_E(TAG, "Invalid content length for OTA binary");
        http.end();
        return false;
    }

    bool canBegin = Update.begin(contentLength, U_FLASH);
    if (!canBegin) {
        LOG_E(TAG, "Not enough space to start OTA update, error: %d", Update.getError());
        http.end();
        return false;
    }

    WiFiClient* stream = http.getStreamPtr();
    size_t written = Update.writeStream(*stream);
    LOG_I(TAG, "Written %d of %d bytes", (int)written, contentLength);

    if (written != (size_t)contentLength) {
        LOG_E(TAG, "Written size mismatch! Written: %d, expected: %d", (int)written, contentLength);
        Update.abort();
        http.end();
        return false;
    }

    if (!Update.end()) {
        LOG_E(TAG, "Error Occurred on Update.end(): %d", Update.getError());
        http.end();
        return false;
    }

    if (!Update.isFinished()) {
        LOG_E(TAG, "Update not finished? Something went wrong.");
        http.end();
        return false;
    }

    LOG_I(TAG, "OTA Update successfully completed! Ready for reboot.");
    http.end();
    return true;
}
