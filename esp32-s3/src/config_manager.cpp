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

    s_config.configured = s_prefs.getBool("configured", false) &&
                          s_prefs.getInt("config_version", 0) == CONFIGURATION_VERSION;

    // 1. Vehicle Wi-Fi
    s_config.vehicle_vin = s_prefs.getString("vehicle_vin", "");
    s_config.wifi_ssid = s_prefs.getString("wifi_ssid", DEFAULT_PHEV_WIFI_SSID);
    s_config.wifi_pass = s_prefs.getString("wifi_pass", DEFAULT_PHEV_WIFI_PASSWORD);
    s_config.battery_max_soc = s_prefs.getInt("bat_max_soc", DEFAULT_BATTERY_MAX_SOC);

    // 2. Cellular & SIM
    s_config.lte_apn = s_prefs.getString("lte_apn", DEFAULT_LTE_APN);
    s_config.sim_pin = s_prefs.getString("sim_pin", "");
    s_config.lte_user = s_prefs.getString("lte_user", DEFAULT_LTE_USER);
    s_config.lte_pass = s_prefs.getString("lte_pass", DEFAULT_LTE_PASS);

    // 3. MQTT & Home Assistant
    s_config.mqtt_server = s_prefs.getString("mqtt_server", DEFAULT_MQTT_SERVER);
    s_config.mqtt_port = s_prefs.getInt("mqtt_port", DEFAULT_MQTT_PORT);
    s_config.mqtt_path = s_prefs.getString("mqtt_path", "/mqtt");
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
    s_prefs.putInt("config_version", CONFIGURATION_VERSION);

    // 1. Vehicle Wi-Fi
    s_prefs.putString("vehicle_vin", cfg.vehicle_vin);
    s_prefs.putString("wifi_ssid", cfg.wifi_ssid);
    s_prefs.putString("wifi_pass", cfg.wifi_pass);
    s_prefs.putInt("bat_max_soc", cfg.battery_max_soc);

    // 2. Cellular & SIM
    s_prefs.putString("lte_apn", cfg.lte_apn);
    s_prefs.putString("sim_pin", cfg.sim_pin);
    s_prefs.putString("lte_user", cfg.lte_user);
    s_prefs.putString("lte_pass", cfg.lte_pass);

    // 3. MQTT & Home Assistant
    s_prefs.putString("mqtt_server", cfg.mqtt_server);
    s_prefs.putInt("mqtt_port", cfg.mqtt_port);
    s_prefs.putString("mqtt_path", cfg.mqtt_path);
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
    String html = String(R"rawliteral(<!doctype html>
<html lang="hu">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Outlander Connect Beállítás</title>
<style>
:root{color-scheme:dark;--bg:#0b111e;--card:#151f30;--accent:#38bdf8;--accent-hover:#0284c7;--text:#f8fafc;--muted:#94a3b8;--border:#26354a;--input:#0c1422;--success:#10b981}
*{box-sizing:border-box;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
body{margin:0;background:var(--bg);color:var(--text);padding:20px;display:grid;place-items:center;min-height:100vh}
main{max-width:460px;width:100%;background:var(--card);border:1px solid var(--border);border-radius:20px;padding:26px;box-shadow:0 24px 64px rgba(0,0,0,.6)}
.header{display:flex;align-items:center;gap:12px;margin-bottom:12px}
.logo{width:36px;height:36px;border-radius:10px;background:linear-gradient(135deg,#0284c7,#38bdf8);display:flex;align-items:center;justify-content:center;font-size:18px}
h1{font-size:22px;margin:0;font-weight:700}
p{color:var(--muted);font-size:13.5px;line-height:1.5;margin:6px 0 16px}
.form-group{margin-bottom:16px}
.label-row{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
label{color:var(--text);font-size:13px;font-weight:600;display:block}
.btn-scan{background:rgba(56,189,248,.15);border:1px solid rgba(56,189,248,.3);color:var(--accent);font-size:12px;padding:4px 10px;border-radius:6px;cursor:pointer;font-weight:600}
.btn-scan:disabled{opacity:.5}
.input-wrap{position:relative;display:flex;align-items:center}
input,select{width:100%;padding:12px 14px;border:1px solid var(--border);border-radius:10px;background:var(--input);color:var(--text);font-size:15px;outline:none;transition:border-color .2s}
input:focus,select:focus{border-color:var(--accent)}
.btn-toggle-pw{position:absolute;right:10px;background:transparent;border:none;color:var(--muted);cursor:pointer;padding:6px;font-size:16px}
.tip{font-size:11.5px;color:var(--muted);margin-top:5px;line-height:1.4}
button.submit-btn{width:100%;margin-top:10px;padding:14px;border:0;border-radius:12px;background:var(--accent);color:#041a2f;font-weight:700;font-size:16px;cursor:pointer;transition:background .2s}
button.submit-btn:hover{background:#7dd3fc}
button.submit-btn:disabled{opacity:.6;cursor:not-allowed}
#status{min-height:24px;color:#f87171;margin-top:14px;font-size:13.5px;text-align:center;line-height:1.4}
#status.loading{color:var(--accent)}
#status.success{color:var(--success)}
</style>
</head>
<body><main>
<div class="header">
  <div class="logo">⚡</div>
  <div>
    <h1>Outlander Connect</h1>
  </div>
</div>
<p>Add meg a jármű alvázszámát (VIN), a párosítási kódot, valamint az autó gyári Wi-Fi adatait a csatlakozáshoz.</p>

<form id="pair-form">
  <div class="form-group">
    <label for="vin">Jármű Alvázszám (VIN)</label>
    <input id="vin" name="vin" minlength="17" maxlength="17" placeholder="pl. JMBXDGG2W..." autocomplete="off" style="text-transform:uppercase" required>
    <div class="tip">17 karakteres azonosító a forgalmiban vagy a szélvédő alatt.</div>
  </div>

  <div class="form-group">
    <label for="pairingCode">Párosítási Kód (Pairing Code)</label>
    <input id="pairingCode" name="pairingCode" placeholder="pl. AB12CD34" autocomplete="off" style="text-transform:uppercase" required>
    <div class="tip">Az Outlander Connect appban vagy a modul dobozán található kód.</div>
  </div>

  <div class="form-group">
    <div class="label-row">
      <label for="carWifiSsid">Autó Gyári Wi-Fi Neve (SSID)</label>
      <button type="button" class="btn-scan" id="btn-scan">🔍 Keresés</button>
    </div>
    <select id="scan-select" style="display:none;margin-bottom:8px"></select>
    <input id="carWifiSsid" name="carWifiSsid" placeholder="pl. REMOTE49a8bc" autocomplete="off" required>
    <div class="tip">A szervizkönyvben lévő fehér kártyáról (mindig REMOTE-mal kezdődik).</div>
  </div>

  <div class="form-group">
    <label for="carWifiPassword">Autó Gyári Wi-Fi Jelszava</label>
    <div class="input-wrap">
      <input id="carWifiPassword" name="carWifiPassword" type="password" placeholder="10 vagy 14 karakteres WPA jelszó" autocomplete="off" required>
      <button type="button" class="btn-toggle-pw" id="btn-toggle-pw" aria-label="Jelszó mutatása">👁️</button>
    </div>
    <div class="tip">A gyári fehér kártyán lévő WPA jelszó (kis- és nagybetű érzékeny).</div>
  </div>

  <button id="submit" class="submit-btn" type="submit">Mentés és Csatlakozás</button>
  <div id="status" role="status"></div>
</form>

<form id="provision-form" action="/save" method="POST"></form>
</main>

<script>
const apiBase = ")rawliteral") + DEFAULT_BACKEND_API_URL + R"rawliteral(";
const form = document.getElementById("pair-form");
const button = document.getElementById("submit");
const status = document.getElementById("status");
const btnScan = document.getElementById("btn-scan");
const scanSelect = document.getElementById("scan-select");
const inpSsid = document.getElementById("carWifiSsid");
const inpPw = document.getElementById("carWifiPassword");
const btnTogglePw = document.getElementById("btn-toggle-pw");

btnTogglePw.addEventListener("click", () => {
  if (inpPw.type === "password") {
    inpPw.type = "text";
    btnTogglePw.textContent = "🔒";
  } else {
    inpPw.type = "password";
    btnTogglePw.textContent = "👁️";
  }
});

btnScan.addEventListener("click", async () => {
  btnScan.disabled = true;
  btnScan.textContent = "⏳ Keresés...";
  try {
    const res = await fetch("/scan");
    const nets = await res.json();
    if (Array.isArray(nets) && nets.length > 0) {
      scanSelect.innerHTML = '<option value="">-- Válassz a talált Wi-Fi hálózatok közül --</option>';
      let foundRemote = false;
      nets.forEach(n => {
        if (!n.ssid) return;
        const opt = document.createElement("option");
        opt.value = n.ssid;
        opt.textContent = (n.ssid.startsWith("REMOTE") ? "🚗 " : "📶 ") + n.ssid + (n.rssi ? " (" + n.rssi + " dBm)" : "");
        if (n.ssid.startsWith("REMOTE")) {
          if (!foundRemote) {
            opt.selected = true;
            inpSsid.value = n.ssid;
            foundRemote = true;
          }
        }
        scanSelect.appendChild(opt);
      });
      scanSelect.style.display = "block";
    } else {
      status.className = "";
      status.textContent = "Nem található Wi-Fi hálózat a modul közelében.";
    }
  } catch (e) {
    status.className = "";
    status.textContent = "A hálózatkeresés nem sikerült.";
  } finally {
    btnScan.disabled = false;
    btnScan.textContent = "🔍 Keresés";
  }
});

scanSelect.addEventListener("change", () => {
  if (scanSelect.value) {
    inpSsid.value = scanSelect.value;
  }
});

form.addEventListener("submit", async event => {
  event.preventDefault();
  const vin = document.getElementById("vin").value.trim().toUpperCase();
  const pairingCode = document.getElementById("pairingCode").value.trim().toUpperCase();
  const carSsid = inpSsid.value.trim();
  const carPw = inpPw.value.trim();

  if (vin.length !== 17) {
    status.className = "";
    status.textContent = "Az alvázszámnak (VIN) pontosan 17 karakterből kell állnia!";
    return;
  }
  if (!carSsid) {
    status.className = "";
    status.textContent = "Kérjük, add meg az autó gyári Wi-Fi nevét (REMOTE...)!";
    return;
  }
  if (!carPw) {
    status.className = "";
    status.textContent = "Kérjük, add meg az autó gyári Wi-Fi jelszavát!";
    return;
  }

  button.disabled = true;
  status.className = "loading";
  status.textContent = "Jármű hitelesítése és felhőkapcsolat beállítása…";

  try {
    const response = await fetch(apiBase + "/api/vehicles/lookup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        vin: vin,
        pairingCode: pairingCode,
        carWifiSsid: carSsid,
        carWifiPassword: carPw
      })
    });
    const data = await response.json();
    if (!response.ok || !data.success) throw new Error(data.error || "A párosítás nem sikerült.");
    if (!data.username || !data.password || !data.topic_prefix) throw new Error("A backend nem adott vissza teljes MQTT hitelesítést.");
    if (!data.brokerUrl) throw new Error("A backend MQTT címe nincs beállítva.");

    const broker = new URL(data.brokerUrl);
    if (broker.protocol !== "wss:" || !broker.hostname) throw new Error("A broker címnek wss:// formátumúnak kell lennie.");

    const settings = data.settings || {};
    const values = {
      vehicle_vin: data.vin || vin,
      wifi_ssid: carSsid,
      wifi_pass: carPw,
      lte_apn: settings.lteApn || settings.lte_apn || "",
      mqtt_server: broker.hostname,
      mqtt_port: broker.port || 443,
      mqtt_path: broker.pathname || "/mqtt",
      mqtt_user: data.username,
      mqtt_pass: data.password,
      mqtt_prefix: data.topic_prefix,
      ha_disc: "1"
    };

    const target = document.getElementById("provision-form");
    target.replaceChildren();
    for (const [name, value] of Object.entries(values)) {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = name;
      input.value = String(value);
      target.appendChild(input);
    }

    status.className = "success";
    status.textContent = "✅ Sikeres párosítás! Az eszköz újraindul és csatlakozik az autóhoz...";
    target.submit();
  } catch (error) {
    status.className = "";
    status.textContent = error.message || "Nem érhető el a felhőszerver. Ellenőrizd a mobilinterneted és próbáld újra.";
    button.disabled = false;
  }
});
</script>
</body></html>)rawliteral";
    s_webServer.send(200, "text/html; charset=utf-8", html);
}
void ConfigManager::handleSave() {
    AppConfig cfg = s_config;

    // 1. Wi-Fi
    if (s_webServer.hasArg("vehicle_vin")) cfg.vehicle_vin = s_webServer.arg("vehicle_vin");
    if (s_webServer.hasArg("wifi_ssid")) cfg.wifi_ssid = s_webServer.arg("wifi_ssid");
    if (s_webServer.hasArg("wifi_pass")) cfg.wifi_pass = s_webServer.arg("wifi_pass");
    if (s_webServer.hasArg("bat_max_soc")) cfg.battery_max_soc = s_webServer.arg("bat_max_soc").toInt();

    // 2. Cellular & SIM
    if (s_webServer.hasArg("lte_apn")) cfg.lte_apn = s_webServer.arg("lte_apn");
    if (s_webServer.hasArg("sim_pin")) cfg.sim_pin = s_webServer.arg("sim_pin");
    if (s_webServer.hasArg("lte_user")) cfg.lte_user = s_webServer.arg("lte_user");
    if (s_webServer.hasArg("lte_pass")) cfg.lte_pass = s_webServer.arg("lte_pass");

    // 3. MQTT & HA
    if (s_webServer.hasArg("mqtt_server")) cfg.mqtt_server = s_webServer.arg("mqtt_server");
    if (s_webServer.hasArg("mqtt_port")) cfg.mqtt_port = s_webServer.arg("mqtt_port").toInt();
    if (s_webServer.hasArg("mqtt_path")) cfg.mqtt_path = s_webServer.arg("mqtt_path");
    if (s_webServer.hasArg("mqtt_user")) cfg.mqtt_user = s_webServer.arg("mqtt_user");
    if (s_webServer.hasArg("mqtt_pass")) cfg.mqtt_pass = s_webServer.arg("mqtt_pass");
    if (s_webServer.hasArg("mqtt_prefix")) cfg.mqtt_prefix = s_webServer.arg("mqtt_prefix");
    cfg.ha_discovery = s_webServer.hasArg("ha_disc");

    // 5. OBD2 Bluetooth
    cfg.obd_enabled = s_webServer.hasArg("obd_en");
    if (s_webServer.hasArg("obd_name")) cfg.obd_device_name = s_webServer.arg("obd_name");
    if (s_webServer.hasArg("obd_pin")) cfg.obd_pin = s_webServer.arg("obd_pin");
    if (s_webServer.hasArg("obd_nom_ah")) cfg.obd_nominal_ah = s_webServer.arg("obd_nom_ah").toFloat();

    if (cfg.vehicle_vin.length() != 17 || cfg.wifi_ssid.isEmpty() || cfg.wifi_pass.isEmpty() ||
        cfg.mqtt_server.isEmpty() || cfg.mqtt_port <= 0 || cfg.mqtt_path.isEmpty() || cfg.mqtt_user.isEmpty() ||
        cfg.mqtt_pass.isEmpty() || cfg.mqtt_prefix.isEmpty()) {
        s_webServer.send(400, "text/plain; charset=utf-8", "Hiányos vagy érvénytelen párosítási konfiguráció.");
        return;
    }
    if (!save(cfg)) {
        s_webServer.send(500, "text/plain; charset=utf-8", "A konfiguráció mentése nem sikerült. Próbáld újra.");
        return;
    }

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
