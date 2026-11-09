#include "wireguard_vpn.h"
#include "logger.h"
#include <WireGuard-ESP32.h>

#define TAG "VPN"

static WireGuard s_wg;

WireGuardVPN::WireGuardVPN()
    : m_connected(false),
      m_endpointPort(51820),
      m_lastHandshakeCheck(0) {
}

bool WireGuardVPN::begin(const char* localIP,
                         const char* privateKey,
                         const char* endpointHost,
                         uint16_t endpointPort,
                         const char* peerPublicKey,
                         const char* allowedIPs) {
    m_localIP = localIP;
    m_privateKey = privateKey;
    m_endpointHost = endpointHost;
    m_endpointPort = endpointPort;
    m_peerPublicKey = peerPublicKey;
    m_allowedIPs = allowedIPs;

    LOG_I(TAG, "Initializing WireGuard VPN tunnel...");
    LOG_I(TAG, "Local VPN IP: %s, Endpoint: %s:%d", localIP, endpointHost, endpointPort);

    IPAddress ip;
    if (!ip.fromString(localIP)) {
        LOG_E(TAG, "Invalid local WireGuard IP: %s", localIP);
        return false;
    }

    bool success = s_wg.begin(
        ip,
        privateKey,
        endpointHost,
        peerPublicKey,
        endpointPort
    );

    if (success) {
        LOG_I(TAG, "WireGuard tunnel started successfully!");
        m_connected = true;
    } else {
        LOG_E(TAG, "WireGuard tunnel start failed!");
        m_connected = false;
    }
    return success;
}

void WireGuardVPN::disconnect() {
    if (m_connected) {
        LOG_I(TAG, "Disconnecting WireGuard tunnel");
        s_wg.end();
        m_connected = false;
    }
}

void WireGuardVPN::loop() {
    if (!m_connected) return;

    uint32_t now = millis();
    if (now - m_lastHandshakeCheck > 30000) {
        m_lastHandshakeCheck = now;
        if (!s_wg.is_initialized()) {
            LOG_W(TAG, "WireGuard tunnel deinitialized, restarting...");
            m_connected = false;
            begin(m_localIP.c_str(), m_privateKey.c_str(), m_endpointHost.c_str(),
                  m_endpointPort, m_peerPublicKey.c_str(), m_allowedIPs.c_str());
        }
    }
}
