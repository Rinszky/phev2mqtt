#include "phev_protocol.h"

uint8_t PhevMessage::calcChecksum(const uint8_t* buf, size_t len) {
    uint8_t sum = 0;
    for (size_t i = 0; i < len; i++) {
        sum += buf[i];
    }
    return sum;
}

bool PhevMessage::parse(const uint8_t* buf, size_t len) {
    if (len < 5) return false;
    type = buf[0];
    length = buf[1];
    ack = buf[2];
    reg = buf[3];

    // Total packet length is length + 2 (length is len(Data) + 3)
    size_t expectedTotal = (size_t)length + 2;
    if (len < expectedTotal) return false;

    size_t dataLen = length > 3 ? (length - 3) : 0;
    data.clear();
    if (dataLen > 0) {
        data.insert(data.end(), buf + 4, buf + 4 + dataLen);
    }
    checksum = buf[4 + dataLen];

    uint8_t calculated = calcChecksum(buf, 4 + dataLen);
    return (calculated == checksum);
}

std::vector<uint8_t> PhevMessage::serialize() const {
    std::vector<uint8_t> packet;
    uint8_t lenField = (uint8_t)(data.size() + 3);
    packet.push_back(type);
    packet.push_back(lenField);
    packet.push_back(ack);
    packet.push_back(reg);
    for (uint8_t b : data) {
        packet.push_back(b);
    }
    uint8_t csum = calcChecksum(packet.data(), packet.size());
    packet.push_back(csum);
    return packet;
}

int scaleBatteryLevel(int rawLevel, int maxSoc) {
    if (rawLevel <= 0) return 0;
    if (maxSoc <= 0 || maxSoc >= 100) {
        return rawLevel > 100 ? 100 : rawLevel;
    }
    int scaled = (rawLevel * 100 + maxSoc / 2) / maxSoc;
    if (scaled > 100) scaled = 100;
    return scaled;
}
