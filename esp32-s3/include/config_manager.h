#ifndef CONFIG_MANAGER_H
#define CONFIG_MANAGER_H

#include <Arduino.h>
#include <Preferences.h>
#include <DNSServer.h>
#include <WebServer.h>

// ==============================================================================
// App Configuration Data Structure
// ==============================================================================
struct AppConfig {
    bool configured = false;

    // 1. Vehicle Wi-Fi
    String wifi_ssid;
    String wifi_pass;
    int battery_max_soc = 94;

    // 2. Cellular Modem / SIM
    String lte_apn;
    String sim_pin;
    String lte_user;
    String lte_pass;

    // 3. WireGuard VPN
    bool wg_enabled = true;
    String wg_local_ip;
    String wg_netmask;
    String wg_endpoint_host;
    int wg_endpoint_port = 51820;
    String wg_private_key;
    String wg_peer_pubkey;
    String wg_allowed_ips;

    // 4. MQTT & Home Assistant
    String mqtt_server;
    int mqtt_port = 1883;
    String mqtt_user;
    String mqtt_pass;
    String mqtt_prefix;
    bool ha_discovery = true;

    // 5. OBD2 Bluetooth
    bool obd_enabled = true;
    String obd_device_name;
    String obd_pin;
    String obd_mac;
    float obd_nominal_ah = 38.0f;
};

class ConfigManager {
public:
    static void init();
    static AppConfig load();
    static bool save(const AppConfig& cfg);
    static void reset();

    // Captive Portal AP & Web Setup
    static void startSetupPortal(const char* apSsid = "PHEV-Bridge-Setup");
    static void stopSetupPortal();
    static bool isPortalRunning();
    static void loop();

    static AppConfig& current() { return s_config; }

private:
    static AppConfig s_config;
    static Preferences s_prefs;
    static DNSServer s_dnsServer;
    static WebServer s_webServer;
    static bool s_portalActive;

    static void handleRoot();
    static void handleSave();
    static void handleCaptiveRedirect();
    static void handleScanWifi();
};

#endif // CONFIG_MANAGER_H
