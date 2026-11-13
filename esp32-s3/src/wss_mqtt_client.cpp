#include "wss_mqtt_client.h"

#include <mbedtls/base64.h>
#include <mbedtls/sha1.h>
#include <limits.h>
#include <esp_system.h>

#include "logger.h"

#define TAG "WSS"

namespace {
constexpr char WEBSOCKET_GUID[] = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
constexpr size_t MAX_HANDSHAKE_BYTES = 2048;
constexpr size_t FRAME_CHUNK_SIZE = 256;

bool writeAll(Client& client, const uint8_t* bytes, size_t length) {
    size_t written = 0;
    const uint32_t startedAt = millis();
    while (written < length && millis() - startedAt < 10000) {
        const size_t count = client.write(bytes + written, length - written);
        written += count;
        if (count == 0) delay(1);
    }
    return written == length;
}
}

void WssMqttClient::begin(Client& tlsClient, const char* path) {
    m_tls = &tlsClient;
    m_path = path && path[0] ? path : "/mqtt";
    if (!m_path.startsWith("/")) m_path = "/" + m_path;
}

int WssMqttClient::connect(IPAddress ip, uint16_t port) {
    const String host = ip.toString();
    return connect(host.c_str(), port);
}

int WssMqttClient::connect(const char* host, uint16_t port) {
    stop();
    if (!m_tls || !host || !host[0]) return 0;

    LOG_I(TAG, "Opening TLS WebSocket to %s:%u%s", host, port, m_path.c_str());
    if (!m_tls->connect(host, port)) {
        LOG_W(TAG, "TLS connection failed");
        return 0;
    }

    if (!performHandshake(host, port)) {
        LOG_W(TAG, "WebSocket upgrade failed");
        stop();
        return 0;
    }

    m_upgraded = true;
    m_frameReady = false;
    m_peeked = -1;
    LOG_I(TAG, "Secure WebSocket upgraded for MQTT");
    return 1;
}

bool WssMqttClient::performHandshake(const char* host, uint16_t port) {
    uint8_t nonce[16];
    for (uint8_t& byte : nonce) byte = static_cast<uint8_t>(esp_random());

    unsigned char encodedKey[32];
    size_t encodedKeyLength = 0;
    if (mbedtls_base64_encode(encodedKey, sizeof(encodedKey), &encodedKeyLength,
                              nonce, sizeof(nonce)) != 0) return false;
    encodedKey[encodedKeyLength] = '\0';

    String request;
    request.reserve(320);
    request += "GET ";
    request += m_path;
    request += " HTTP/1.1\r\nHost: ";
    request += host;
    if (port != 443) {
        request += ":";
        request += port;
    }
    request += "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ";
    request += reinterpret_cast<const char*>(encodedKey);
    request += "\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: mqtt\r\n\r\n";
    if (!writeAll(*m_tls, reinterpret_cast<const uint8_t*>(request.c_str()), request.length())) return false;

    String response;
    response.reserve(512);
    const uint32_t startedAt = millis();
    while (millis() - startedAt < 12000 && response.length() < MAX_HANDSHAKE_BYTES) {
        while (m_tls->available() > 0 && response.length() < MAX_HANDSHAKE_BYTES) {
            const int value = m_tls->read();
            if (value < 0) break;
            response += static_cast<char>(value);
            const int length = response.length();
            if (length >= 4 && response.substring(length - 4) == "\r\n\r\n") {
                String lower = response;
                lower.toLowerCase();
                if (!response.startsWith("HTTP/1.1 101") && !response.startsWith("HTTP/1.0 101")) return false;
                if (lower.indexOf("upgrade: websocket") < 0 ||
                    lower.indexOf("connection: upgrade") < 0) return false;

                uint8_t digest[20];
                String acceptInput = String(reinterpret_cast<const char*>(encodedKey)) + WEBSOCKET_GUID;
                if (mbedtls_sha1_ret(reinterpret_cast<const uint8_t*>(acceptInput.c_str()),
                                     acceptInput.length(), digest) != 0) return false;
                unsigned char expectedAccept[32];
                size_t expectedLength = 0;
                if (mbedtls_base64_encode(expectedAccept, sizeof(expectedAccept), &expectedLength,
                                          digest, sizeof(digest)) != 0) return false;
                expectedAccept[expectedLength] = '\0';

                const int headerStart = lower.indexOf("sec-websocket-accept:");
                if (headerStart < 0) return false;
                const int valueStart = response.indexOf(':', headerStart) + 1;
                const int lineEnd = response.indexOf("\r\n", valueStart);
                const String actualAccept = response.substring(valueStart, lineEnd < 0 ? response.length() : lineEnd);
                String trimmedAccept = actualAccept;
                trimmedAccept.trim();
                return trimmedAccept == reinterpret_cast<const char*>(expectedAccept);
            }
        }
        delay(1);
    }
    return false;
}

size_t WssMqttClient::write(uint8_t value) {
    return write(&value, 1);
}

size_t WssMqttClient::write(const uint8_t* buffer, size_t size) {
    if (!m_upgraded || !buffer || size == 0) return 0;
    return sendFrame(0x2, buffer, size) == size ? size : 0;
}

size_t WssMqttClient::sendFrame(uint8_t opcode, const uint8_t* payload, size_t length) {
    if (!m_tls || !m_tls->connected() || length > 65535) return 0;

    uint8_t header[8];
    size_t headerLength = 0;
    header[headerLength++] = 0x80 | (opcode & 0x0f); // FIN + opcode
    if (length <= 125) {
        header[headerLength++] = 0x80 | static_cast<uint8_t>(length);
    } else {
        header[headerLength++] = 0x80 | 126;
        header[headerLength++] = static_cast<uint8_t>((length >> 8) & 0xff);
        header[headerLength++] = static_cast<uint8_t>(length & 0xff);
    }

    uint8_t mask[4];
    for (uint8_t& byte : mask) byte = static_cast<uint8_t>(esp_random());
    memcpy(header + headerLength, mask, sizeof(mask));
    headerLength += sizeof(mask);
    if (!writeAll(*m_tls, header, headerLength)) return 0;

    uint8_t masked[FRAME_CHUNK_SIZE];
    size_t sent = 0;
    while (sent < length) {
        const size_t remaining = length - sent;
        const size_t chunkLength = remaining < FRAME_CHUNK_SIZE ? remaining : FRAME_CHUNK_SIZE;
        for (size_t index = 0; index < chunkLength; ++index) {
            masked[index] = payload[sent + index] ^ mask[(sent + index) & 0x03];
        }
        if (!writeAll(*m_tls, masked, chunkLength)) return 0;
        sent += chunkLength;
    }
    return sent;
}

bool WssMqttClient::readByte(uint8_t& value, uint32_t timeoutMs) {
    if (!m_tls) return false;
    const uint32_t startedAt = millis();
    while (millis() - startedAt < timeoutMs) {
        if (m_tls->available() > 0) {
            const int result = m_tls->read();
            if (result >= 0) {
                value = static_cast<uint8_t>(result);
                return true;
            }
        }
        if (!m_tls->connected()) return false;
        delay(1);
    }
    return false;
}

bool WssMqttClient::readExact(uint8_t* output, size_t length, uint32_t timeoutMs) {
    const uint32_t startedAt = millis();
    for (size_t index = 0; index < length; ++index) {
        const uint32_t elapsed = millis() - startedAt;
        if (elapsed >= timeoutMs || !readByte(output[index], timeoutMs - elapsed)) return false;
    }
    return true;
}

bool WssMqttClient::readFrameHeader() {
    if (!m_tls || !m_tls->connected()) return false;
    uint8_t firstBytes[2];
    if (!readExact(firstBytes, sizeof(firstBytes), 100)) return false;

    const bool finalFrame = (firstBytes[0] & 0x80) != 0;
    m_frameOpcode = firstBytes[0] & 0x0f;
    m_frameMasked = (firstBytes[1] & 0x80) != 0;
    m_frameLength = firstBytes[1] & 0x7f;
    m_frameOffset = 0;
    if ((m_frameOpcode & 0x08) && !finalFrame) {
        LOG_W(TAG, "Broker sent an invalid fragmented WebSocket control frame");
        return false;
    }
    if (m_frameMasked) {
        LOG_W(TAG, "Broker sent an invalid masked WebSocket frame");
        return false;
    }
    if ((m_frameOpcode & 0x08) == 0 && m_frameOpcode != 0x0 && m_frameOpcode != 0x2) {
        LOG_W(TAG, "Broker sent unsupported WebSocket opcode %u", m_frameOpcode);
        return false;
    }

    if (m_frameLength == 126) {
        uint8_t extended[2];
        if (!readExact(extended, sizeof(extended), 100)) return false;
        m_frameLength = (static_cast<uint16_t>(extended[0]) << 8) | extended[1];
    } else if (m_frameLength == 127) {
        uint8_t extended[8];
        if (!readExact(extended, sizeof(extended), 100)) return false;
        m_frameLength = 0;
        for (uint8_t byte : extended) m_frameLength = (m_frameLength << 8) | byte;
    }

    if (m_frameLength > 65535) return false;
    m_frameReady = true;
    return true;
}

bool WssMqttClient::consumeControlFrame() {
    uint8_t controlPayload[125];
    if (m_frameLength > sizeof(controlPayload)) return false;
    uint8_t mask[4] = {};
    if (m_frameMasked && !readExact(mask, sizeof(mask), 100)) return false;
    if (!readExact(controlPayload, m_frameLength, 500)) return false;
    if (m_frameMasked) {
        for (uint64_t index = 0; index < m_frameLength; ++index) controlPayload[index] ^= mask[index & 0x03];
    }

    const uint8_t opcode = m_frameOpcode;
    m_frameReady = false;
    if (opcode == 0x8) {
        m_upgraded = false;
        return false;
    }
    if (opcode == 0x9) return sendFrame(0xA, controlPayload, m_frameLength) == m_frameLength;
    return true;
}

int WssMqttClient::available() {
    if (!m_upgraded || !m_tls || !m_tls->connected()) return 0;
    if (m_peeked >= 0) return 1;

    while (true) {
        if (!m_frameReady && !readFrameHeader()) return 0;
        if (m_frameOpcode & 0x08) {
            if (!consumeControlFrame()) return 0;
            continue;
        }
        if (m_frameLength == 0) {
            m_frameReady = false;
            continue;
        }
        break;
    }
    if (m_tls->available() <= 0) return 0;
    const uint64_t buffered = static_cast<uint64_t>(m_tls->available());
    const uint64_t readable = m_frameLength < buffered ? m_frameLength : buffered;
    return readable > INT_MAX ? INT_MAX : static_cast<int>(readable);
}

int WssMqttClient::read() {
    if (m_peeked >= 0) {
        const int value = m_peeked;
        m_peeked = -1;
        return value;
    }
    if (available() <= 0) return -1;

    const int value = m_tls->read();
    if (value < 0) return -1;
    if (m_frameLength > 0) --m_frameLength;
    if (m_frameLength == 0) m_frameReady = false;
    return value;
}

int WssMqttClient::read(uint8_t* buffer, size_t size) {
    size_t count = 0;
    while (count < size) {
        const int value = read();
        if (value < 0) break;
        buffer[count++] = static_cast<uint8_t>(value);
    }
    return static_cast<int>(count);
}

int WssMqttClient::peek() {
    if (m_peeked < 0) m_peeked = read();
    return m_peeked;
}

void WssMqttClient::flush() {
    if (m_tls) m_tls->flush();
}

void WssMqttClient::stop() {
    m_upgraded = false;
    m_frameReady = false;
    m_peeked = -1;
    if (m_tls) m_tls->stop();
}

uint8_t WssMqttClient::connected() {
    return m_upgraded && m_tls && m_tls->connected();
}
