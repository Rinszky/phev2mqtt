#include <Arduino.h>
#include "config.h"
#include "logger.h"
#include "phev_client.h"
#include "cellular_modem.h"
#include "wireguard_vpn.h"
#include "mqtt_manager.h"
#include "power_manager.h"
#include "remote_cli.h"

#define TAG "MAIN"

static PhevClient    s_phev;
static CellularModem s_modem;
static WireGuardVPN  s_vpn;
static MqttManager   s_mqtt;
static PowerManager  s_power;
static RemoteCLI     s_cli;

void setup() {
    // 1. Initialize Serial and Logger
    Serial.begin(115200);
    Logger::init(LOG_BUFFER_SIZE);
    delay(1000);

    LOG_I(TAG, "=================================================");
    LOG_I(TAG, " Mitsubishi Outlander PHEV ESP32-S3 Bridge Boot  ");
    LOG_I(TAG, " Hardware: ESP32-S3 Dual STA Wi-Fi + LTE + VPN   ");
    LOG_I(TAG, "=================================================");

    // 2. Power Management & 12V Battery Sense
    s_power.begin(&s_phev, &s_modem);
    float v12 = s_power.read12VVoltage();
    LOG_I(TAG, "12V Auxiliary Battery: %.2f V", v12);

    // 3. Cellular Modem Initialization (LTE Internet)
    LOG_I(TAG, "Starting LTE Cellular Modem...");
    bool lteOk = s_modem.begin(DEFAULT_LTE_APN, DEFAULT_LTE_USER, DEFAULT_LTE_PASS);
    if (!lteOk) {
        LOG_W(TAG, "LTE Modem initialization failed or no SIM. Will retry in loop.");
    }

    // 4. WireGuard VPN Tunnel
    LOG_I(TAG, "Starting WireGuard VPN Tunnel to Home Network...");
    s_vpn.begin(
        DEFAULT_WG_LOCAL_IP,
        DEFAULT_WG_PRIVATE_KEY,
        DEFAULT_WG_ENDPOINT_HOST,
        DEFAULT_WG_ENDPOINT_PORT,
        DEFAULT_WG_PEER_PUBKEY,
        DEFAULT_WG_ALLOWED_IPS
    );

    // 5. Connect to Car Wi-Fi Access Point (Station Mode)
    LOG_I(TAG, "Connecting to Vehicle Wi-Fi AP (%s)...", DEFAULT_PHEV_WIFI_SSID);
    s_phev.setBatteryMaxSoc(DEFAULT_BATTERY_MAX_SOC);
    s_phev.begin(DEFAULT_PHEV_WIFI_SSID, DEFAULT_PHEV_WIFI_PASSWORD);

    // 6. Connect to Home MQTT Broker (routed through WireGuard VPN)
    Client* netClient = s_modem.getNetworkClient();
    if (netClient) {
        s_mqtt.begin(
            *netClient,
            DEFAULT_MQTT_SERVER,
            DEFAULT_MQTT_PORT,
            DEFAULT_MQTT_USER,
            DEFAULT_MQTT_PASS,
            DEFAULT_MQTT_PREFIX,
            &s_phev
        );
        s_mqtt.setHAEnabled(DEFAULT_HA_DISCOVERY);
        s_mqtt.setHAPrefix(DEFAULT_HA_PREFIX);
    }

    // 7. Remote CLI Maintenance Shell (over VPN)
    s_cli.begin(REMOTE_CLI_PORT, REMOTE_CLI_PASSWORD, &s_phev, &s_modem, &s_power);

    LOG_I(TAG, "System initialization complete. Entering main loop.");
}

void loop() {
    // 1. Maintain Cellular Modem
    s_modem.loop();

    // 2. Maintain WireGuard VPN Tunnel
    s_vpn.loop();

    // 3. Maintain Car Wi-Fi TCP Session & Protocol Polling
    s_phev.loop();

    // 4. Maintain MQTT Connection & Command Handling
    s_mqtt.loop();

    // 5. Remote CLI Shell Service
    s_cli.loop();

    // 6. 12V Battery Protection & Duty Cycle Sleep Monitoring
    s_power.loop();

    // Yield to FreeRTOS watchdog
    delay(10);
}
