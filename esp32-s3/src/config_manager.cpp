#include "config_manager.h"
#include "config.h"
#include "logger.h"
#include <WiFi.h>

#define TAG "CFG"

AppConfig ConfigManager::s_config;
Preferences ConfigManager::s_prefs;
DNSServer ConfigManager::s_dnsServer;
WebServer ConfigManager::s_webServer(80);
bool ConfigManager::s_portalActive = false;

void ConfigManager::init() {
    load();
}

AppConfig ConfigManager::load() {
    s_prefs.begin("phev_cfg", true); // read-only mode

    s_config.configured = s_prefs.getBool("configured", false);

    // 1. Vehicle Wi-Fi
    s_config.wifi_ssid = s_prefs.getString("wifi_ssid", DEFAULT_PHEV_WIFI_SSID);
    s_config.wifi_pass = s_prefs.getString("wifi_pass", DEFAULT_PHEV_WIFI_PASSWORD);
    s_config.battery_max_soc = s_prefs.getInt("bat_max_soc", DEFAULT_BATTERY_MAX_SOC);

    // 2. Cellular & SIM
    s_config.lte_apn = s_prefs.getString("lte_apn", DEFAULT_LTE_APN);
    s_config.sim_pin = s_prefs.getString("sim_pin", "");
    s_config.lte_user = s_prefs.getString("lte_user", DEFAULT_LTE_USER);
    s_config.lte_pass = s_prefs.getString("lte_pass", DEFAULT_LTE_PASS);

    // 3. WireGuard VPN
    s_config.wg_enabled = s_prefs.getBool("wg_en", true);
    s_config.wg_local_ip = s_prefs.getString("wg_local_ip", DEFAULT_WG_LOCAL_IP);
    s_config.wg_netmask = s_prefs.getString("wg_netmask", DEFAULT_WG_NETMASK);
    s_config.wg_endpoint_host = s_prefs.getString("wg_ep_host", DEFAULT_WG_ENDPOINT_HOST);
    s_config.wg_endpoint_port = s_prefs.getInt("wg_ep_port", DEFAULT_WG_ENDPOINT_PORT);
    s_config.wg_private_key = s_prefs.getString("wg_priv_key", DEFAULT_WG_PRIVATE_KEY);
    s_config.wg_peer_pubkey = s_prefs.getString("wg_peer_pub", DEFAULT_WG_PEER_PUBKEY);
    s_config.wg_allowed_ips = s_prefs.getString("wg_allow_ips", DEFAULT_WG_ALLOWED_IPS);

    // 4. MQTT & Home Assistant
    s_config.mqtt_server = s_prefs.getString("mqtt_server", DEFAULT_MQTT_SERVER);
    s_config.mqtt_port = s_prefs.getInt("mqtt_port", DEFAULT_MQTT_PORT);
    s_config.mqtt_user = s_prefs.getString("mqtt_user", DEFAULT_MQTT_USER);
    s_config.mqtt_pass = s_prefs.getString("mqtt_pass", DEFAULT_MQTT_PASS);
    s_config.mqtt_prefix = s_prefs.getString("mqtt_prefix", DEFAULT_MQTT_PREFIX);
    s_config.ha_discovery = s_prefs.getBool("ha_disc", DEFAULT_HA_DISCOVERY);

    // 5. OBD2 Bluetooth
    s_config.obd_enabled = s_prefs.getBool("obd_en", OBD_BLUETOOTH_ENABLED);
    s_config.obd_device_name = s_prefs.getString("obd_name", OBD_BLUETOOTH_DEVICE_NAME);
    s_config.obd_pin = s_prefs.getString("obd_pin", OBD_BLUETOOTH_PIN);
    s_config.obd_mac = s_prefs.getString("obd_mac", OBD_BLUETOOTH_MAC);
    s_config.obd_nominal_ah = s_prefs.getFloat("obd_nom_ah", OBD_NOMINAL_BATTERY_AH);

    s_prefs.end();

    LOG_I(TAG, "Config loaded from NVS. Configured: %s, Car SSID: %s, APN: %s",
          s_config.configured ? "YES" : "NO (Defaults)",
          s_config.wifi_ssid.c_str(), s_config.lte_apn.c_str());

    return s_config;
}

bool ConfigManager::save(const AppConfig& cfg) {
    if (!s_prefs.begin("phev_cfg", false)) { // read-write mode
        LOG_E(TAG, "Failed to open NVS preferences for writing!");
        return false;
    }

    s_prefs.putBool("configured", true);

    // 1. Vehicle Wi-Fi
    s_prefs.putString("wifi_ssid", cfg.wifi_ssid);
    s_prefs.putString("wifi_pass", cfg.wifi_pass);
    s_prefs.putInt("bat_max_soc", cfg.battery_max_soc);

    // 2. Cellular & SIM
    s_prefs.putString("lte_apn", cfg.lte_apn);
    s_prefs.putString("sim_pin", cfg.sim_pin);
    s_prefs.putString("lte_user", cfg.lte_user);
    s_prefs.putString("lte_pass", cfg.lte_pass);

    // 3. WireGuard VPN
    s_prefs.putBool("wg_en", cfg.wg_enabled);
    s_prefs.putString("wg_local_ip", cfg.wg_local_ip);
    s_prefs.putString("wg_netmask", cfg.wg_netmask);
    s_prefs.putString("wg_ep_host", cfg.wg_endpoint_host);
    s_prefs.putInt("wg_ep_port", cfg.wg_endpoint_port);
    s_prefs.putString("wg_priv_key", cfg.wg_private_key);
    s_prefs.putString("wg_peer_pub", cfg.wg_peer_pubkey);
    s_prefs.putString("wg_allow_ips", cfg.wg_allowed_ips);

    // 4. MQTT & Home Assistant
    s_prefs.putString("mqtt_server", cfg.mqtt_server);
    s_prefs.putInt("mqtt_port", cfg.mqtt_port);
    s_prefs.putString("mqtt_user", cfg.mqtt_user);
    s_prefs.putString("mqtt_pass", cfg.mqtt_pass);
    s_prefs.putString("mqtt_prefix", cfg.mqtt_prefix);
    s_prefs.putBool("ha_disc", cfg.ha_discovery);

    // 5. OBD2 Bluetooth
    s_prefs.putBool("obd_en", cfg.obd_enabled);
    s_prefs.putString("obd_name", cfg.obd_device_name);
    s_prefs.putString("obd_pin", cfg.obd_pin);
    s_prefs.putString("obd_mac", cfg.obd_mac);
    s_prefs.putFloat("obd_nom_ah", cfg.obd_nominal_ah);

    s_prefs.end();
    s_config = cfg;
    s_config.configured = true;

    LOG_I(TAG, "New configuration successfully committed to NVS!");
    return true;
}

void ConfigManager::reset() {
    s_prefs.begin("phev_cfg", false);
    s_prefs.clear();
    s_prefs.end();
    s_config.configured = false;
    LOG_W(TAG, "NVS preferences cleared. Reset to factory defaults.");
}

void ConfigManager::startSetupPortal(const char* apSsid) {
    if (s_portalActive) return;

    LOG_I(TAG, "Starting Captive Portal Setup Hotspot: '%s'...", apSsid);

    WiFi.mode(WIFI_AP_STA);
    WiFi.softAP(apSsid);

    IPAddress apIP = WiFi.softAPIP();
    LOG_I(TAG, "Setup Hotspot IP: %s", apIP.toString().c_str());

    // Start DNS server on port 53 for captive portal redirect
    s_dnsServer.setErrorReplyCode(DNSReplyCode::NoError);
    s_dnsServer.start(53, "*", apIP);

    // Web Server endpoints
    s_webServer.on("/", HTTP_GET, handleRoot);
    s_webServer.on("/save", HTTP_POST, handleSave);
    s_webServer.on("/scan", HTTP_GET, handleScanWifi);

    // Captive portal detection URLs
    s_webServer.on("/generate_204", HTTP_GET, handleCaptiveRedirect); // Android
    s_webServer.on("/gen_204", HTTP_GET, handleCaptiveRedirect);
    s_webServer.on("/hotspot-detect.html", HTTP_GET, handleCaptiveRedirect); // Apple iOS/macOS
    s_webServer.on("/ncsi.txt", HTTP_GET, handleCaptiveRedirect); // Windows
    s_webServer.onNotFound(handleCaptiveRedirect);

    s_webServer.begin();
    s_portalActive = true;
    LOG_I(TAG, "Captive Portal Web Server started on port 80!");
}

void ConfigManager::stopSetupPortal() {
    if (!s_portalActive) return;
    s_dnsServer.stop();
    s_webServer.stop();
    WiFi.softAPdisconnect(true);
    s_portalActive = false;
    LOG_I(TAG, "Setup Hotspot and Web Portal stopped.");
}

bool ConfigManager::isPortalRunning() {
    return s_portalActive;
}

void ConfigManager::loop() {
    if (s_portalActive) {
        s_dnsServer.processNextRequest();
        s_webServer.handleClient();
    }
}

void ConfigManager::handleCaptiveRedirect() {
    s_webServer.sendHeader("Location", "http://192.168.4.1/", true);
    s_webServer.send(302, "text/plain", "");
}

void ConfigManager::handleScanWifi() {
    int n = WiFi.scanNetworks();
    String json = "[";
    for (int i = 0; i < n; i++) {
        if (i > 0) json += ",";
        json += "{\"ssid\":\"" + WiFi.SSID(i) + "\",\"rssi\":" + String(WiFi.RSSI(i)) + "}";
    }
    json += "]";
    s_webServer.send(200, "application/json", json);
}

void ConfigManager::handleRoot() {
    String html = R"rawliteral(<!DOCTYPE html>
<html lang="hu">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PHEV-Bridge Beállítás</title>
<style>
  :root { --bg: #121820; --card: #1c2532; --accent: #00d26a; --text: #e2e8f0; --sub: #94a3b8; --input: #0f172a; --border: #334155; }
  * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  body { background: var(--bg); color: var(--text); padding: 16px; display: flex; justify-content: center; }
  .container { max-width: 540px; width: 100%; }
  .header { text-align: center; margin-bottom: 24px; padding-top: 10px; }
  .header h1 { font-size: 22px; color: #fff; margin-bottom: 6px; }
  .header p { font-size: 13px; color: var(--sub); }
  .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; padding: 18px; margin-bottom: 16px; box-shadow: 0 4px 6px rgba(0,0,0,0.3); }
  .card h2 { font-size: 15px; color: var(--accent); margin-bottom: 14px; display: flex; align-items: center; gap: 8px; border-bottom: 1px solid var(--border); padding-bottom: 8px; }
  .form-group { margin-bottom: 14px; }
  .form-group:last-child { margin-bottom: 0; }
  label { display: block; font-size: 12px; font-weight: 600; color: var(--sub); margin-bottom: 5px; text-transform: uppercase; letter-spacing: 0.5px; }
  input[type="text"], input[type="password"], input[type="number"], select {
    width: 100%; padding: 10px 12px; background: var(--input); border: 1px solid var(--border); border-radius: 8px; color: #fff; font-size: 14px; outline: none; transition: border-color 0.2s;
  }
  input:focus, select:focus { border-color: var(--accent); }
  .row { display: flex; gap: 12px; }
  .row .form-group { flex: 1; }
  .checkbox-group { display: flex; align-items: center; gap: 10px; margin-top: 8px; cursor: pointer; }
  .checkbox-group input { width: 18px; height: 18px; accent-color: var(--accent); cursor: pointer; }
  .checkbox-group label { margin: 0; cursor: pointer; text-transform: none; font-size: 14px; color: var(--text); }
  .btn-submit { width: 100%; background: var(--accent); color: #000; font-weight: bold; border: none; padding: 14px; border-radius: 10px; font-size: 16px; cursor: pointer; margin-top: 10px; margin-bottom: 30px; transition: transform 0.1s, opacity 0.2s; }
  .btn-submit:hover { opacity: 0.9; }
  .btn-submit:active { transform: scale(0.98); }
  .note { font-size: 11px; color: var(--sub); margin-top: 4px; }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <h1>🚗 Outlander PHEV Bridge</h1>
    <p>Hardveres Beállítás és Konfiguráció</p>
  </div>
  <form action="/save" method="POST">
    <!-- 1. Jármű Wi-Fi -->
    <div class="card">
      <h2>🚗 1. Jármű Wi-Fi Beállítások</h2>
      <div class="form-group">
        <label>Autó Wi-Fi Neve (SSID):</label>
        <input type="text" name="wifi_ssid" value=")rawliteral" + s_config.wifi_ssid + R"rawliteral(" required placeholder="REMOTExxxxxx">
        <div class="note">Az autó saját Wi-Fi hálózata (általában REMOTE-tal kezdődik)</div>
      </div>
      <div class="form-group">
        <label>Autó Wi-Fi Jelszava:</label>
        <input type="password" name="wifi_pass" value=")rawliteral" + s_config.wifi_pass + R"rawliteral(" required>
      </div>
      <div class="form-group">
        <label>Akkumulátor 100% Korrekció (%-ban):</label>
        <input type="number" name="bat_max_soc" min="80" max="100" value=")rawliteral" + String(s_config.battery_max_soc) + R"rawliteral(">
        <div class="note">A gyári 94%-os maximális töltöttséget simán 100%-ra skálázza</div>
      </div>
    </div>

    <!-- 2. Mobilnet & SIM -->
    <div class="card">
      <h2>📶 2. Mobilnet (4G LTE) & SIM</h2>
      <div class="form-group">
        <label>APN Név (Szolgáltató):</label>
        <input type="text" name="lte_apn" value=")rawliteral" + s_config.lte_apn + R"rawliteral(" placeholder="net">
        <div class="note">Telekom / Yettel: "net", Vodafone / Digi: "internet"</div>
      </div>
      <div class="form-group">
        <label>SIM Kártya PIN Kódja:</label>
        <input type="password" name="sim_pin" value=")rawliteral" + s_config.sim_pin + R"rawliteral(" placeholder="Üres, ha nincs PIN">
        <div class="note">Hagyd üresen, ha a kártyán nincs PIN-kód kérés!</div>
      </div>
      <div class="row">
        <div class="form-group">
          <label>APN Felhasználó:</label>
          <input type="text" name="lte_user" value=")rawliteral" + s_config.lte_user + R"rawliteral(">
        </div>
        <div class="form-group">
          <label>APN Jelszó:</label>
          <input type="password" name="lte_pass" value=")rawliteral" + s_config.lte_pass + R"rawliteral(">
        </div>
      </div>
    </div>

    <!-- 3. WireGuard VPN -->
    <div class="card">
      <h2>🛡️ 3. WireGuard VPN (Otthoni Alagút)</h2>
      <div class="form-group checkbox-group">
        <input type="checkbox" id="wg_en" name="wg_en" value="1" )rawliteral" + String(s_config.wg_enabled ? "checked" : "") + R"rawliteral(>
        <label for="wg_en">WireGuard VPN Bekapcsolása</label>
      </div>
      <div class="form-group" style="margin-top:12px;">
        <label>Otthoni Végpont (Host / DDNS / Fix IP):</label>
        <input type="text" name="wg_ep_host" value=")rawliteral" + s_config.wg_endpoint_host + R"rawliteral(" placeholder="home.example.com">
      </div>
      <div class="row">
        <div class="form-group">
          <label>Végpont Port:</label>
          <input type="number" name="wg_ep_port" value=")rawliteral" + String(s_config.wg_endpoint_port) + R"rawliteral(">
        </div>
        <div class="form-group">
          <label>ESP32 Belső VPN IP:</label>
          <input type="text" name="wg_local_ip" value=")rawliteral" + s_config.wg_local_ip + R"rawliteral(" placeholder="10.0.0.50">
        </div>
      </div>
      <div class="form-group">
        <label>ESP32 Privát Kulcs (Base64):</label>
        <input type="text" name="wg_priv_key" value=")rawliteral" + s_config.wg_private_key + R"rawliteral(">
      </div>
      <div class="form-group">
        <label>Otthoni Szerver Publikus Kulcs:</label>
        <input type="text" name="wg_peer_pub" value=")rawliteral" + s_config.wg_peer_pubkey + R"rawliteral(">
      </div>
      <div class="form-group">
        <label>Engedélyezett Alhálózatok (Allowed IPs):</label>
        <input type="text" name="wg_allow_ips" value=")rawliteral" + s_config.wg_allowed_ips + R"rawliteral(" placeholder="10.0.0.0/24,192.168.1.0/24">
      </div>
    </div>

    <!-- 4. Home Assistant & MQTT -->
    <div class="card">
      <h2>🏠 4. Home Assistant & MQTT Broker</h2>
      <div class="row">
        <div class="form-group" style="flex: 2;">
          <label>MQTT Broker IP / Hoszt:</label>
          <input type="text" name="mqtt_server" value=")rawliteral" + s_config.mqtt_server + R"rawliteral(" placeholder="10.0.0.1 vagy 192.168.1.x">
        </div>
        <div class="form-group" style="flex: 1;">
          <label>Port:</label>
          <input type="number" name="mqtt_port" value=")rawliteral" + String(s_config.mqtt_port) + R"rawliteral(">
        </div>
      </div>
      <div class="row">
        <div class="form-group">
          <label>Felhasználónév:</label>
          <input type="text" name="mqtt_user" value=")rawliteral" + s_config.mqtt_user + R"rawliteral(">
        </div>
        <div class="form-group">
          <label>Jelszó:</label>
          <input type="password" name="mqtt_pass" value=")rawliteral" + s_config.mqtt_pass + R"rawliteral(">
        </div>
      </div>
      <div class="form-group">
        <label>MQTT Topic Előtag:</label>
        <input type="text" name="mqtt_prefix" value=")rawliteral" + s_config.mqtt_prefix + R"rawliteral(" placeholder="phev">
      </div>
      <div class="form-group checkbox-group">
        <input type="checkbox" id="ha_disc" name="ha_disc" value="1" )rawliteral" + String(s_config.ha_discovery ? "checked" : "") + R"rawliteral(>
        <label for="ha_disc">Home Assistant Auto-Discovery Engedélyezése</label>
      </div>
    </div>

    <!-- 5. Bluetooth OBD2 -->
    <div class="card">
      <h2>🔍 5. Bluetooth OBD2 (PHEV Watchdog)</h2>
      <div class="form-group checkbox-group">
        <input type="checkbox" id="obd_en" name="obd_en" value="1" )rawliteral" + String(s_config.obd_enabled ? "checked" : "") + R"rawliteral(>
        <label for="obd_en">OBD2 Cella- és Akkudiagnosztika Bekapcsolása</label>
      </div>
      <div class="row" style="margin-top:12px;">
        <div class="form-group" style="flex: 2;">
          <label>Bluetooth Adapter Neve:</label>
          <input type="text" name="obd_name" value=")rawliteral" + s_config.obd_device_name + R"rawliteral(" placeholder="OBDII">
        </div>
        <div class="form-group" style="flex: 1;">
          <label>Párosítási PIN:</label>
          <input type="text" name="obd_pin" value=")rawliteral" + s_config.obd_pin + R"rawliteral(" placeholder="1234">
        </div>
      </div>
      <div class="form-group">
        <label>Névleges Akkumulátor Kapacitás (Ah):</label>
        <input type="number" step="0.1" name="obd_nom_ah" value=")rawliteral" + String(s_config.obd_nominal_ah, 1) + R"rawliteral(">
        <div class="note">38.0 Ah (2013-2018 / 12 kWh) vagy 46.0 Ah (2019+ / 13.8 kWh)</div>
      </div>
    </div>

    <button type="submit" class="btn-submit">💾 Beállítások Mentése és Újraindítás</button>
  </form>
</div>
</body>
</html>)rawliteral";

    s_webServer.send(200, "text/html", html);
}

void ConfigManager::handleSave() {
    AppConfig cfg = s_config;

    // 1. Wi-Fi
    if (s_webServer.hasArg("wifi_ssid")) cfg.wifi_ssid = s_webServer.arg("wifi_ssid");
    if (s_webServer.hasArg("wifi_pass")) cfg.wifi_pass = s_webServer.arg("wifi_pass");
    if (s_webServer.hasArg("bat_max_soc")) cfg.battery_max_soc = s_webServer.arg("bat_max_soc").toInt();

    // 2. Cellular & SIM
    if (s_webServer.hasArg("lte_apn")) cfg.lte_apn = s_webServer.arg("lte_apn");
    if (s_webServer.hasArg("sim_pin")) cfg.sim_pin = s_webServer.arg("sim_pin");
    if (s_webServer.hasArg("lte_user")) cfg.lte_user = s_webServer.arg("lte_user");
    if (s_webServer.hasArg("lte_pass")) cfg.lte_pass = s_webServer.arg("lte_pass");

    // 3. WireGuard VPN
    cfg.wg_enabled = s_webServer.hasArg("wg_en");
    if (s_webServer.hasArg("wg_ep_host")) cfg.wg_endpoint_host = s_webServer.arg("wg_ep_host");
    if (s_webServer.hasArg("wg_ep_port")) cfg.wg_endpoint_port = s_webServer.arg("wg_ep_port").toInt();
    if (s_webServer.hasArg("wg_local_ip")) cfg.wg_local_ip = s_webServer.arg("wg_local_ip");
    if (s_webServer.hasArg("wg_priv_key")) cfg.wg_private_key = s_webServer.arg("wg_priv_key");
    if (s_webServer.hasArg("wg_peer_pub")) cfg.wg_peer_pubkey = s_webServer.arg("wg_peer_pub");
    if (s_webServer.hasArg("wg_allow_ips")) cfg.wg_allowed_ips = s_webServer.arg("wg_allow_ips");

    // 4. MQTT & HA
    if (s_webServer.hasArg("mqtt_server")) cfg.mqtt_server = s_webServer.arg("mqtt_server");
    if (s_webServer.hasArg("mqtt_port")) cfg.mqtt_port = s_webServer.arg("mqtt_port").toInt();
    if (s_webServer.hasArg("mqtt_user")) cfg.mqtt_user = s_webServer.arg("mqtt_user");
    if (s_webServer.hasArg("mqtt_pass")) cfg.mqtt_pass = s_webServer.arg("mqtt_pass");
    if (s_webServer.hasArg("mqtt_prefix")) cfg.mqtt_prefix = s_webServer.arg("mqtt_prefix");
    cfg.ha_discovery = s_webServer.hasArg("ha_disc");

    // 5. OBD2 Bluetooth
    cfg.obd_enabled = s_webServer.hasArg("obd_en");
    if (s_webServer.hasArg("obd_name")) cfg.obd_device_name = s_webServer.arg("obd_name");
    if (s_webServer.hasArg("obd_pin")) cfg.obd_pin = s_webServer.arg("obd_pin");
    if (s_webServer.hasArg("obd_nom_ah")) cfg.obd_nominal_ah = s_webServer.arg("obd_nom_ah").toFloat();

    save(cfg);

    String resp = R"rawliteral(<!DOCTYPE html>
<html lang="hu">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sikeres Mentés</title>
<style>
  body { background: #121820; color: #fff; font-family: sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; text-align: center; }
  .box { background: #1c2532; padding: 30px; border-radius: 12px; border: 1px solid #334155; max-width: 400px; }
  h1 { color: #00d26a; font-size: 24px; margin-bottom: 12px; }
  p { color: #94a3b8; font-size: 14px; line-height: 1.5; }
</style>
</head>
<body>
<div class="box">
  <h1>✅ Sikeres Mentés!</h1>
  <p>A beállítások elmentve az ESP32 belső Flash memóriájába.<br><br>Az eszköz most <b>újraindul</b> és megkezdi a csatlakozást az autóhoz és a hálózathoz...</p>
</div>
<script>setTimeout(function(){ window.close(); }, 5000);</script>
</body>
</html>)rawliteral";

    s_webServer.send(200, "text/html", resp);

    delay(2000);
    ESP.restart();
}
