#ifndef PHEV_CONFIG_H
#define PHEV_CONFIG_H

#include <Arduino.h>

// ==============================================================================
// 1. VEHICLE WI-FI SETTINGS (Direct connection to Outlander PHEV AP)
// ==============================================================================
// The car broadcasts an SSID typically named "REMOTE..." (case-sensitive)
#define DEFAULT_PHEV_WIFI_SSID       "REMOTExxxxxx"
#define DEFAULT_PHEV_WIFI_PASSWORD   "xxxxxxxxxxxxxx"
#define PHEV_CAR_IP                  "192.168.8.46"
#define PHEV_CAR_PORT                8080
#define PHEV_PING_INTERVAL_MS        1500
#define PHEV_POLL_INTERVAL_MS        30000

// ==============================================================================
// 2. LTE CELLULAR MODEM (UART & Power Control)
// ==============================================================================
// Supported modules: SIM7600, SIM7000, A7670, EC25, BG96
#define TINY_GSM_MODEM_SIM7600       // Define your modem family
#define MODEM_UART_NUM               1
#define MODEM_TX_PIN                 17 // ESP32-S3 TX -> Modem RX
#define MODEM_RX_PIN                 18 // ESP32-S3 RX -> Modem TX
#define MODEM_PWRKEY_PIN             4  // Pulse to power ON/OFF
#define MODEM_RESET_PIN              5  // Hardware reset pin
#define MODEM_DTR_PIN                6  // Sleep / wake control (DTR high=sleep)
#define MODEM_STATUS_PIN             7  // Status indicator pin
#define MODEM_BAUDRATE               115200

// Cellular APN (e.g. "internet", "online", "vodafone", "telekom")
#define DEFAULT_LTE_APN              "internet"
#define DEFAULT_LTE_USER             ""
#define DEFAULT_LTE_PASS             ""

// ==============================================================================
// 3. WIREGUARD VPN CONFIGURATION (Tunnel to Home Network)
// ==============================================================================
// Ultra-lightweight native VPN allowing secure, NAT-traversing access to home LAN
#define DEFAULT_WG_LOCAL_IP          "10.0.0.50"
#define DEFAULT_WG_NETMASK           "255.255.255.0"
#define DEFAULT_WG_PRIVATE_KEY       "aA11...=" // ESP32 base64 private key
#define DEFAULT_WG_ENDPOINT_HOST     "home.example.com" // Home WAN IP or DDNS
#define DEFAULT_WG_ENDPOINT_PORT     51820
#define DEFAULT_WG_PEER_PUBKEY       "bB22...=" // Home server base64 public key
#define DEFAULT_WG_ALLOWED_IPS       "10.0.0.0/24,192.168.1.0/24"

// ==============================================================================
// 4. MQTT & HOME ASSISTANT CONFIGURATION
// ==============================================================================
// MQTT broker is reached via the WireGuard VPN tunnel
#define DEFAULT_MQTT_SERVER          "10.0.0.1" // or "192.168.1.100" (home broker)
#define DEFAULT_MQTT_PORT            1883
#define DEFAULT_MQTT_USER            "phev_user"
#define DEFAULT_MQTT_PASS            "phev_pass"
#define DEFAULT_MQTT_PREFIX          "phev"
#define DEFAULT_HA_DISCOVERY         true
#define DEFAULT_HA_PREFIX            "homeassistant"
#define DEFAULT_BATTERY_MAX_SOC      94 // Scale 94% raw BMU reading smoothly to 100%

// ==============================================================================
// 5. 12V AUXILIARY BATTERY PROTECTION & LOW-POWER MODES
// ==============================================================================
// Protects the car's 35-45Ah 12V lead-acid / AGM auxiliary battery.
// Voltage divider on ADC1 (e.g. 100k Ohm high-side + 10k Ohm low-side -> 11:1)
#define BATTERY_12V_ADC_PIN          1  // GPIO1 (ADC1_CH0)
#define BATTERY_12V_DIVIDER_RATIO    11.0f
#define BATTERY_12V_LOW_CUTOFF       11.90f // Below 11.9V: Emergency deep sleep
#define BATTERY_12V_SAFE_RESTART     12.60f // Wake only when alternator/charger charges it

// Inactivity threshold: If not plugged into EV charger or driven for 3-4 days
#define INACTIVITY_SLEEP_HOURS       72 // 3 days (72 hours)
#define DUTY_CYCLE_SLEEP_MINUTES     30 // Sleep for 30 minutes in low-power mode
#define DUTY_CYCLE_WAKE_SECONDS      45 // Wake for 45s to poll car and check MQTT commands

// ==============================================================================
// 6. REMOTE MAINTENANCE, CLI SHELL & OTA
// ==============================================================================
#define REMOTE_CLI_PORT              2222 // Telnet/TCP CLI shell over VPN tunnel
#define REMOTE_CLI_PASSWORD          "phevadmin"
#define LOG_BUFFER_SIZE              8192 // In-memory ring buffer for live log dump

#endif // PHEV_CONFIG_H
