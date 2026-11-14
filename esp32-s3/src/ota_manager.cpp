#include "ota_manager.h"
#include "logger.h"
#include <Update.h>
#include <mbedtls/sha256.h>

#define TAG "OTA"

namespace {

bool parseHttpUrl(const String& url, bool& isHttps, String& host, uint16_t& port, String& path) {
    String u = url;
    u.trim();
    if (u.startsWith("https://")) {
        isHttps = true;
        u = u.substring(8);
        port = 443;
    } else if (u.startsWith("http://")) {
        isHttps = false;
        u = u.substring(7);
        port = 80;
    } else {
        return false;
    }

    int slashIdx = u.indexOf('/');
    String hostPort = (slashIdx >= 0) ? u.substring(0, slashIdx) : u;
    path = (slashIdx >= 0) ? u.substring(slashIdx) : "/";

    int colonIdx = hostPort.indexOf(':');
    if (colonIdx >= 0) {
        host = hostPort.substring(0, colonIdx);
        port = (uint16_t)hostPort.substring(colonIdx + 1).toInt();
    } else {
        host = hostPort;
    }
    return host.length() > 0 && port > 0;
}

String readHttpLine(Client* client, uint32_t timeoutMs = 15000) {
    String line;
    uint32_t start = millis();
    while (millis() - start < timeoutMs) {
        if (client->available()) {
            char c = (char)client->read();
            if (c == '\n') {
                if (line.endsWith("\r")) line.remove(line.length() - 1);
                return line;
            }
            line += c;
        } else if (!client->connected()) {
            break;
        } else {
            delay(5);
        }
    }
    return line;
}

} // namespace

bool OTAManager::performOTA(CellularModem* modem,
                           const char* binaryUrl,
                           const char* expectedSha256,
                           OtaProgressCallback progressCb) {
    auto report = [&](const char* state, int progress, const char* err = nullptr) {
        if (progressCb) progressCb(state, progress, err);
    };

    if (!modem || !modem->isConnected()) {
        LOG_E(TAG, "LTE modem is not connected, cannot start OTA");
        report("error", 0, "LTE modem offline");
        return false;
    }

    bool isHttps = true;
    String host, path;
    uint16_t port = 443;
    if (!binaryUrl || !parseHttpUrl(String(binaryUrl), isHttps, host, port, path)) {
        LOG_E(TAG, "Invalid OTA URL: %s", binaryUrl ? binaryUrl : "(null)");
        report("error", 0, "Invalid firmware URL");
        return false;
    }

    LOG_I(TAG, "Starting LTE OTA (%s://%s:%d%s)", isHttps ? "https" : "http", host.c_str(), port, path.c_str());
    report("downloading", 0);

    Client* client = modem->createOtaClient(isHttps);
    if (!client) {
        LOG_E(TAG, "Failed to allocate LTE client for OTA");
        report("error", 0, "Modem client error");
        return false;
    }

    if (!client->connect(host.c_str(), port)) {
        LOG_E(TAG, "Connection to %s:%d failed", host.c_str(), port);
        modem->destroyOtaClient(client, isHttps);
        report("error", 0, "Connection failed");
        return false;
    }

    String req = "GET " + path + " HTTP/1.1\r\n" +
                 "Host: " + host + "\r\n" +
                 "User-Agent: OutlanderPHEV-ESP32-OTA/1.0\r\n" +
                 "Connection: close\r\n\r\n";
    client->print(req);

    // Parse status line
    String statusLine = readHttpLine(client);
    int statusCode = 0;
    int firstSpace = statusLine.indexOf(' ');
    if (firstSpace > 0) {
        statusCode = statusLine.substring(firstSpace + 1).toInt();
    }
    LOG_I(TAG, "HTTP status: %s (code=%d)", statusLine.c_str(), statusCode);
    if (statusCode != 200) {
        modem->destroyOtaClient(client, isHttps);
        report("error", 0, "HTTP error");
        return false;
    }

    // Parse headers
    int contentLength = -1;
    while (true) {
        String h = readHttpLine(client);
        if (h.length() == 0) break;
        String lower = h;
        lower.toLowerCase();
        if (lower.startsWith("content-length:")) {
            contentLength = lower.substring(15).toInt();
        }
    }

    if (contentLength <= 0) {
        LOG_E(TAG, "Invalid Content-Length: %d", contentLength);
        modem->destroyOtaClient(client, isHttps);
        report("error", 0, "Invalid firmware size");
        return false;
    }

    LOG_I(TAG, "Firmware size: %d bytes", contentLength);
    if (!Update.begin(contentLength, U_FLASH)) {
        LOG_E(TAG, "Update.begin failed, error=%d", Update.getError());
        modem->destroyOtaClient(client, isHttps);
        report("error", 0, "Not enough flash space");
        return false;
    }

    mbedtls_sha256_context shaCtx;
    mbedtls_sha256_init(&shaCtx);
    mbedtls_sha256_starts(&shaCtx, 0);

    uint8_t buf[1024];
    int totalRead = 0;
    int lastReportedPct = 0;
    uint32_t lastDataAt = millis();

    while (totalRead < contentLength) {
        int avail = client->available();
        if (avail > 0) {
            int toRead = min((int)sizeof(buf), min(avail, contentLength - totalRead));
            int n = client->read(buf, toRead);
            if (n > 0) {
                lastDataAt = millis();
                mbedtls_sha256_update(&shaCtx, buf, n);
                size_t written = Update.write(buf, n);
                if (written != (size_t)n) {
                    LOG_E(TAG, "Flash write mismatch at %d/%d", totalRead, contentLength);
                    mbedtls_sha256_free(&shaCtx);
                    Update.abort();
                    modem->destroyOtaClient(client, isHttps);
                    report("error", lastReportedPct, "Flash write error");
                    return false;
                }
                totalRead += n;
                int pct = (int)(((int64_t)totalRead * 100) / contentLength);
                if (pct - lastReportedPct >= 10 || pct == 100) {
                    lastReportedPct = pct;
                    LOG_I(TAG, "OTA download progress: %d%% (%d/%d)", pct, totalRead, contentLength);
                    report("downloading", pct);
                }
            }
        } else {
            if (!client->connected()) break;
            if (millis() - lastDataAt > 30000) {
                LOG_E(TAG, "OTA stream timeout at %d/%d", totalRead, contentLength);
                break;
            }
            delay(5);
        }
        yield();
    }

    modem->destroyOtaClient(client, isHttps);

    if (totalRead != contentLength) {
        LOG_E(TAG, "Incomplete OTA download: %d / %d", totalRead, contentLength);
        mbedtls_sha256_free(&shaCtx);
        Update.abort();
        report("error", lastReportedPct, "Incomplete download");
        return false;
    }

    uint8_t hash[32];
    mbedtls_sha256_finish(&shaCtx, hash);
    mbedtls_sha256_free(&shaCtx);

    char hexHash[65];
    for (int i = 0; i < 32; i++) {
        snprintf(hexHash + i * 2, 3, "%02x", hash[i]);
    }
    hexHash[64] = '\0';
    LOG_I(TAG, "Downloaded SHA-256: %s", hexHash);

    if (expectedSha256 && strlen(expectedSha256) == 64) {
        if (strcasecmp(hexHash, expectedSha256) != 0) {
            LOG_E(TAG, "SHA-256 mismatch! Expected=%s, Got=%s", expectedSha256, hexHash);
            Update.abort();
            report("error", 100, "SHA-256 checksum mismatch");
            return false;
        }
    }

    report("flashing", 100);
    if (!Update.end(true) || !Update.isFinished()) {
        LOG_E(TAG, "Update.end failed, error=%d", Update.getError());
        report("error", 100, "Firmware verification failed");
        return false;
    }

    LOG_I(TAG, "OTA Update successful! Rebooting...");
    report("rebooting", 100);
    delay(600);
    ESP.restart();
    return true;
}
