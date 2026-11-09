#ifndef WIREGUARD_VPN_H
#define WIREGUARD_VPN_H

#include <Arduino.h>
#include <IPAddress.h>

class WireGuardVPN {
public:
    WireGuardVPN();

    bool begin(const char* localIP,
               const char* privateKey,
               const char* endpointHost,
               uint16_t endpointPort,
               const char* peerPublicKey,
               const char* allowedIPs = "0.0.0.0/0");
    void loop();

    bool isConnected() const { return m_connected; }
    void disconnect();

private:
    bool m_connected;
    String m_localIP;
    String m_privateKey;
    String m_endpointHost;
    uint16_t m_endpointPort;
    String m_peerPublicKey;
    String m_allowedIPs;
    uint32_t m_lastHandshakeCheck;
};

#endif // WIREGUARD_VPN_H
