#include <Arduino.h>
#include "config.h"
#include "config_manager.h"
#include "logger.h"
#include "phev_client.h"
#include "cellular_modem.h"
#include "wireguard_vpn.h"
#include "mqtt_manager.h"
#include "power_manager.h"
#include "remote_cli.h"
#include "obd_reader.h"

#define TAG "MAIN"

static PhevClient    s_phev;
static CellularModem s_modem;
static WireGuardVPN  s_vpn;
static MqttManager   s_mqtt;
static PowerManager  s_power;
static RemoteCLI     s_cli;
static ObdReader     s_obd;

static uint32_t s_bootTime = 0;
static bool s_failsafeApStarted = false;

void setup() {
    // 1. Initialize Serial and Logger
    Serial.begin(115200);
    Logger::init(LOG_BUFFER_SIZE);
    delay(1000);

    LOG_I(TAG, "=================================================");
    LOG_I(TAG, " Mitsubishi Outlander PHEV ESP32 Bridge Boot     ");
    LOG_I(TAG, " Hardware: LilyGO T-A7670E (ESP32-WROVER-E LTE)  ");
    LOG_I(TAG, " Features: Wi-Fi Remote + GPS + OBD2 + Web Setup ");
    LOG_I(TAG, "=================================================");

    // 2. Load Configuration from NVS Flash
    ConfigManager::init();
    const AppConfig& cfg = ConfigManager::current();

    // 3. First-boot / Unconfigured check: Start Captive Portal Hotspot
    if (!cfg.configured) {
        LOG_W(TAG, "Device not yet configured! Starting Setup Hotspot 'PHEV-Bridge-Setup'...");
        ConfigManager::startSetupPortal("PHEV-Bridge-Setup");
        while (ConfigManager::isPortalRunning()) {
            ConfigManager::loop();
            delay(10);
        }
    }

    s_bootTime = millis();

    // 4. Power Management
    s_power.begin(&s_phev, &s_modem);
    float v12 = s_power.read12VVoltage();
    LOG_I(TAG, "Battery Voltage: %.2f V", v12);

    // 5. Cellular Modem Initialization (LTE Internet & SIM)
    LOG_I(TAG, "Starting LTE Cellular Modem (APN: %s)...", cfg.lte_apn.c_str());
    bool lteOk = s_modem.begin(cfg.lte_apn.c_str(), cfg.lte_user.c_str(), cfg.lte_pass.c_str(), cfg.sim_pin.c_str());
    if (!lteOk) {
        LOG_W(TAG, "LTE Modem initialization failed or no SIM. Will retry in loop.");
    }

    // 6. WireGuard VPN Tunnel
    if (cfg.wg_enabled && cfg.wg_endpoint_host.length() > 0) {
        LOG_I(TAG, "Starting WireGuard VPN Tunnel to %s:%d...", cfg.wg_endpoint_host.c_str(), cfg.wg_endpoint_port);
        s_vpn.begin(
            cfg.wg_local_ip.c_str(),
            cfg.wg_private_key.c_str(),
            cfg.wg_endpoint_host.c_str(),
            cfg.wg_endpoint_port,
            cfg.wg_peer_pubkey.c_str(),
            cfg.wg_allowed_ips.c_str()
        );
    } else {
        LOG_I(TAG, "WireGuard VPN is disabled or unconfigured.");
    }

    // 7. Connect to Car Wi-Fi Access Point (Station Mode)
    LOG_I(TAG, "Connecting to Vehicle Wi-Fi AP (%s)...", cfg.wifi_ssid.c_str());
    s_phev.setBatteryMaxSoc(cfg.battery_max_soc);
    s_phev.begin(cfg.wifi_ssid.c_str(), cfg.wifi_pass.c_str());

    // 8. Bluetooth OBD2 Telemetry Reader (BMU Diagnostics & Watchdog)
    if (cfg.obd_enabled) {
        LOG_I(TAG, "Starting Bluetooth OBD2 Telemetry Service (%s)...", cfg.obd_device_name.c_str());
        s_obd.begin(cfg.obd_device_name.c_str(), cfg.obd_pin.c_str(), cfg.obd_mac.c_str(), cfg.obd_nominal_ah);
    }

    // 9. Connect to Home MQTT Broker (routed through WireGuard VPN)
    Client* netClient = s_modem.getNetworkClient();
    if (netClient) {
        s_mqtt.begin(
            *netClient,
            cfg.mqtt_server.c_str(),
            cfg.mqtt_port,
            cfg.mqtt_user.c_str(),
            cfg.mqtt_pass.c_str(),
            cfg.mqtt_prefix.c_str(),
            &s_phev,
            &s_modem,
            &s_obd
        );
        s_mqtt.setHAEnabled(cfg.ha_discovery);
        s_mqtt.setHAPrefix(DEFAULT_HA_PREFIX);
    }

    // 10. Remote CLI Maintenance Shell (over VPN)
    s_cli.begin(REMOTE_CLI_PORT, REMOTE_CLI_PASSWORD, &s_phev, &s_modem, &s_power, &s_obd);

    LOG_I(TAG, "System initialization complete. Entering main loop.");
}

void loop() {
    // 0. Maintain Captive Portal Web Server (if active)
    ConfigManager::loop();

    // Fail-safe: If car Wi-Fi cannot connect for 3 minutes from boot, activate Setup Hotspot in parallel!
    if (!s_failsafeApStarted && !s_phev.isWifiConnected() && (millis() - s_bootTime > 180000)) {
        s_failsafeApStarted = true;
        LOG_W(TAG, "Car Wi-Fi not found after 3 minutes! Launching fail-safe Setup Hotspot 'PHEV-Bridge-Setup'...");
        ConfigManager::startSetupPortal("PHEV-Bridge-Setup");
    }

    // 1. Maintain Cellular Modem
    s_modem.loop();

    // 2. Maintain WireGuard VPN Tunnel
    s_vpn.loop();

    // 3. Maintain Car Wi-Fi TCP Session & Protocol Polling
    s_phev.loop();

    // 4. Maintain OBD2 Bluetooth Diagnostic Session
    s_obd.loop();

    // 5. Maintain MQTT Connection & Command Handling
    s_mqtt.loop();

    // 6. Remote CLI Shell Service
    s_cli.loop();

    // 7. Battery Protection & Duty Cycle Sleep Monitoring
    s_power.loop();

    // Yield to FreeRTOS watchdog
    delay(10);
}
