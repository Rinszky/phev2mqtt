#ifndef WSS_MQTT_CLIENT_H
#define WSS_MQTT_CLIENT_H

#include <Arduino.h>
#include <Client.h>

class WssMqttClient final : public Client {
public:
    void begin(Client& tlsClient, const char* path);

    int connect(IPAddress ip, uint16_t port) override;
    int connect(const char* host, uint16_t port) override;
    size_t write(uint8_t value) override;
    size_t write(const uint8_t* buffer, size_t size) override;
    int available() override;
    int read() override;
    int read(uint8_t* buffer, size_t size) override;
    int peek() override;
    void flush() override;
    void stop() override;
    uint8_t connected() override;
    operator bool() override { return connected(); }

private:
    Client* m_tls = nullptr;
    String m_path = "/mqtt";
    bool m_upgraded = false;
    bool m_frameReady = false;
    bool m_frameMasked = false;
    uint8_t m_frameOpcode = 0;
    uint64_t m_frameLength = 0;
    uint64_t m_frameOffset = 0;
    int m_peeked = -1;

    bool performHandshake(const char* host, uint16_t port);
    bool readFrameHeader();
    bool readExact(uint8_t* output, size_t length, uint32_t timeoutMs);
    bool readByte(uint8_t& value, uint32_t timeoutMs);
    size_t sendFrame(uint8_t opcode, const uint8_t* payload, size_t length);
    bool consumeControlFrame();
};

#endif
