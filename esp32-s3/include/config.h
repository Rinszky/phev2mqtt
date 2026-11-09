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
// 2. LTE CELLULAR MODEM (LilyGO T-A7670E R2 Pinout & Power Control)
// ==============================================================================
#ifndef TINY_GSM_MODEM_SIM7600
#define TINY_GSM_MODEM_SIM7600       // SIMCom A7670E uses SIM7600 AT command set in TinyGSM
#endif
#define MODEM_UART_NUM               1
#define MODEM_TX_PIN                 26 // LilyGO Pin 26 (ESP32 RX <- Modem TX)
#define MODEM_RX_PIN                 27 // LilyGO Pin 27 (ESP32 TX -> Modem RX)
#define MODEM_PWRKEY_PIN             4  // LilyGO Pin 4 (PWRKEY pulse)
#define MODEM_RESET_PIN              5  // LilyGO Pin 5 (Hardware Reset)
#define MODEM_DTR_PIN                25 // LilyGO Pin 25 (Sleep/Wake control)
#define MODEM_POWERON_PIN            12 // LilyGO Pin 12 (Board peripheral power enable)
#define MODEM_RING_PIN               33 // LilyGO Pin 33 (Ring indicator)
#define MODEM_BAUDRATE               115200

// Cellular APN (Telekom: "net", Yettel: "net", Vodafone: "internet", Digi: "internet")
#define DEFAULT_LTE_APN              "net"
#define DEFAULT_LTE_USER             ""
#define DEFAULT_LTE_PASS             ""

// ==============================================================================
// 3. GPS / GNSS VEHICLE TRACKING (Home Assistant Device Tracker)
// ==============================================================================
#define GPS_ENABLED                  true
#define GPS_POLL_INTERVAL_SEC        15 // Send GPS update to Home Assistant every 15s

// ==============================================================================
// 4. WIREGUARD VPN CONFIGURATION (Tunnel to Home Network)
// ==============================================================================
#define DEFAULT_WG_LOCAL_IP          "10.0.0.50"
#define DEFAULT_WG_NETMASK           "255.255.255.0"
#define DEFAULT_WG_PRIVATE_KEY       "aA11...=" // ESP32 base64 private key
#define DEFAULT_WG_ENDPOINT_HOST     "home.example.com" // Home WAN IP or DDNS
#define DEFAULT_WG_ENDPOINT_PORT     51820
#define DEFAULT_WG_PEER_PUBKEY       "bB22...=" // Home server base64 public key
#define DEFAULT_WG_ALLOWED_IPS       "10.0.0.0/24,192.168.1.0/24"

// ==============================================================================
// 5. MQTT & HOME ASSISTANT CONFIGURATION
// ==============================================================================
#define DEFAULT_MQTT_SERVER          "10.0.0.1" // or "192.168.1.100" (home broker via VPN)
#define DEFAULT_MQTT_PORT            1883
#define DEFAULT_MQTT_USER            "phev_user"
#define DEFAULT_MQTT_PASS            "phev_pass"
#define DEFAULT_MQTT_PREFIX          "phev"
#define DEFAULT_HA_DISCOVERY         true
#define DEFAULT_HA_PREFIX            "homeassistant"
#define DEFAULT_BATTERY_MAX_SOC      94 // Scale 94% raw BMU reading smoothly to 100%

// ==============================================================================
// 6. POWER & BATTERY MONITORING
// ==============================================================================
// On LilyGO T-A7670E, GPIO 35 is connected to the onboard lithium battery circuit (2:1 divider)
#define BATTERY_ADC_PIN              35
#define BATTERY_DIVIDER_RATIO        2.0f
#define BATTERY_LOW_CUTOFF           3.30f // For LiPo/18650 cell (< 3.3V)

// PowerManager compatibility aliases
#define BATTERY_12V_ADC_PIN          BATTERY_ADC_PIN
#define BATTERY_12V_DIVIDER_RATIO    BATTERY_DIVIDER_RATIO
#define BATTERY_12V_LOW_CUTOFF       BATTERY_LOW_CUTOFF

// Inactivity threshold: If not plugged into EV charger or driven for 3-4 days
#define INACTIVITY_SLEEP_HOURS       72 // 3 days (72 hours)
#define DUTY_CYCLE_SLEEP_MINUTES     15 // Sleep for 15 minutes in low-power mode
#define DUTY_CYCLE_WAKE_SECONDS      45 // Wake for 45s to poll car and check MQTT commands

// ==============================================================================
// 7. REMOTE MAINTENANCE, CLI SHELL & OTA
// ==============================================================================
#define REMOTE_CLI_PORT              2222 // Telnet/TCP CLI shell over VPN tunnel
#define REMOTE_CLI_PASSWORD          "phevadmin"
#define LOG_BUFFER_SIZE              8192 // In-memory ring buffer for live log dump

// ==============================================================================
// 8. OBD2 BLUETOOTH TELEMETRY (PHEV Watchdog & BMU Battery Diagnostics)
// ==============================================================================
#define OBD_BLUETOOTH_ENABLED        true
#define OBD_BLUETOOTH_DEVICE_NAME    "OBDII"      // Bluetooth name of ELM327 adapter (e.g. "OBDII", "vgate", "Android-Vlink")
#define OBD_BLUETOOTH_PIN            "1234"       // Bluetooth pairing PIN ("1234" or "0000")
#define OBD_BLUETOOTH_MAC            ""           // Optional: specify MAC "AA:BB:CC:DD:EE:FF" to bind to specific adapter
#define OBD_POLL_INTERVAL_SEC        10           // Telemetry poll interval while driving or charging (seconds)
#define OBD_NOMINAL_BATTERY_AH       38.0f        // 38.0 Ah for 12 kWh (2013-2018), 46.0 Ah for 13.8 kWh (2019+)

#endif // PHEV_CONFIG_H
