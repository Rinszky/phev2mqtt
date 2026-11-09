#include "remote_cli.h"
#include "logger.h"
#include "ota_manager.h"
#include "config_manager.h"

#define TAG "CLI"

RemoteCLI::RemoteCLI()
    : m_server(2222),
      m_authenticated(false),
      m_phev(nullptr),
      m_modem(nullptr),
      m_pwr(nullptr),
      m_obd(nullptr) {
}

void RemoteCLI::begin(uint16_t port, const char* password,
                      PhevClient* phev, CellularModem* modem, PowerManager* pwr, ObdReader* obd) {
    m_password = password;
    m_phev = phev;
    m_modem = modem;
    m_pwr = pwr;
    m_obd = obd;
    m_server = WiFiServer(port);
    m_server.begin();
    LOG_I(TAG, "Remote CLI maintenance shell listening on port %d", port);
}

void RemoteCLI::loop() {
    if (m_server.hasClient()) {
        if (!m_activeClient || !m_activeClient.connected()) {
            m_activeClient = m_server.available();
            m_authenticated = false;
            m_cmdBuffer = "";
            m_activeClient.println("=================================================");
            m_activeClient.println(" Mitsubishi Outlander PHEV ESP32-S3 Bridge Shell");
            m_activeClient.println("=================================================");
            m_activeClient.print("Password: ");
        } else {
            WiFiClient rejected = m_server.available();
            rejected.println("Busy: Another session is active.");
            rejected.stop();
        }
    }

    if (m_activeClient && m_activeClient.connected()) {
        handleClient();
    }
}

void RemoteCLI::printPrompt() {
    if (m_activeClient && m_activeClient.connected()) {
        m_activeClient.print("\nphev2mqtt> ");
    }
}

void RemoteCLI::handleClient() {
    while (m_activeClient.available() > 0) {
        char c = (char)m_activeClient.read();
        if (c == '\r') continue;

        if (c == '\n') {
            String line = m_cmdBuffer;
            line.trim();
            m_cmdBuffer = "";

            if (!m_authenticated) {
                if (line == m_password) {
                    m_authenticated = true;
                    m_activeClient.println("\nAuthentication successful! Type 'help' for commands.");
                    printPrompt();
                } else {
                    m_activeClient.println("\nInvalid password!");
                    m_activeClient.print("Password: ");
                }
            } else {
                if (line.length() > 0) {
                    executeCommand(line);
                }
                printPrompt();
            }
        } else if (c == 0x08 || c == 0x7f) { // Backspace
            if (m_cmdBuffer.length() > 0) {
                m_cmdBuffer.remove(m_cmdBuffer.length() - 1);
                m_activeClient.print("\b \b");
            }
        } else {
            m_cmdBuffer += c;
            if (m_authenticated) {
                m_activeClient.print(c); // Echo
            } else {
                m_activeClient.print('*');
            }
        }
    }
}

void RemoteCLI::executeCommand(const String& cmd) {
    m_activeClient.println();

    if (cmd == "help" || cmd == "?") {
        m_activeClient.println("Available Commands:");
        m_activeClient.println("  status               - Show vehicle, cellular, 12V and memory state");
        m_activeClient.println("  logs                 - Dump real-time in-memory log ring buffer");
        m_activeClient.println("  clearlogs            - Clear log buffer");
        m_activeClient.println("  climate heat [dur]   - Start pre-heating (dur: 10/20/30 min)");
        m_activeClient.println("  climate cool [dur]   - Start pre-cooling (dur: 10/20/30 min)");
        m_activeClient.println("  climate stop         - Stop pre-AC");
        m_activeClient.println("  doors lock           - Lock vehicle doors");
        m_activeClient.println("  doors unlock         - Unlock vehicle doors");
        m_activeClient.println("  lights head [on|off] - Control headlights");
        m_activeClient.println("  lights park [on|off] - Control parking lights");
        m_activeClient.println("  locate               - Sound horn & hazards");
        m_activeClient.println("  reboot               - Safe reboot ESP32-S3");
        m_activeClient.println("  sleep                - Force duty-cycle low power sleep now");
        m_activeClient.println("  ota <http_url>       - Trigger remote firmware OTA update");
        m_activeClient.println("  obd                  - Show live Bluetooth OBD2/BMU diagnostics (SOH, cells, kW)");
        m_activeClient.println("  setup                - Start Web Captive Portal Hotspot for configuration");
        m_activeClient.println("  config               - Display current stored configuration");
        m_activeClient.println("  factoryreset         - Wipe stored configuration and reboot into Setup mode");
        m_activeClient.println("  exit / quit          - Close session");
    } else if (cmd == "status") {
        m_activeClient.printf("Firmware: ESP32-S3 PHEV Bridge (Dual STA+LTE)\n");
        m_activeClient.printf("Uptime:   %u seconds\n", millis() / 1000);
        m_activeClient.printf("Free Heap:%u bytes, Free PSRAM: %u bytes\n", ESP.getFreeHeap(), ESP.getFreePsram());
        
        if (m_pwr) {
            float v = m_pwr->read12VVoltage();
            m_activeClient.printf("12V Battery: %.2f V (Power Mode: %d, Inactive: %u h)\n",
                                  v, (int)m_pwr->getPowerMode(), m_pwr->getHoursSinceLastCharge());
        }

        if (m_modem) {
            m_activeClient.printf("LTE Modem:   %s (CSQ: %d, Op: %s)\n",
                                  m_modem->isConnected() ? "Connected" : "Disconnected",
                                  m_modem->getSignalQuality(), m_modem->getOperator().c_str());
            if (m_modem->isGPSFix()) {
                m_activeClient.printf("GPS Location:Lat=%.6f, Lon=%.6f, Speed=%.1f km/h, Sats=%d\n",
                                      m_modem->getLatitude(), m_modem->getLongitude(),
                                      m_modem->getSpeed(), m_modem->getSatellites());
            } else {
                m_activeClient.printf("GPS Status:  Acquiring fix / No satellite lock\n");
            }
        }

        if (m_obd) {
            m_activeClient.printf("OBD2 Link:   %s (ECU/BMU: %s)\n",
                                  m_obd->isConnected() ? "Connected" : "Disconnected",
                                  m_obd->isEcuResponding() ? "Responding / Active" : "Sleeping / No Data");
        }

        if (m_phev) {
            const BatteryStatus& b = m_phev->getBatteryStatus();
            const VehicleDoors& d = m_phev->getDoors();
            const ChargingStatus& c = m_phev->getChargingStatus();
            ClimateTracker& cl = m_phev->getClimateTracker();

            m_activeClient.printf("Car Wi-Fi:   %s, Car TCP: %s\n",
                                  m_phev->isWifiConnected() ? "Connected" : "Disconnected",
                                  m_phev->isCarConnected() ? "Connected" : "Disconnected");
            m_activeClient.printf("VIN:         %s, ECU: %s\n",
                                  m_phev->getVIN().c_str(), m_phev->getECUVersion().c_str());
            m_activeClient.printf("Traction Bat:%d%% (Raw: %d%%, MaxSoc: %d%%)\n",
                                  b.scaledLevel, b.rawLevel, m_phev->getBatteryMaxSoc());
            m_activeClient.printf("Charging:    %s, Plugged: %s, Rem: %d min (ETA: %s)\n",
                                  c.charging ? "YES" : "NO", c.plugged ? "YES" : "NO",
                                  c.remainingMinutes, c.completionTime.c_str());
            m_activeClient.printf("Doors:       Locked: %s, Driver: %s, Headlights: %s\n",
                                  d.locked ? "YES" : "NO", d.driverOpen ? "OPEN" : "CLOSED",
                                  d.headlights ? "ON" : "OFF");
            m_activeClient.printf("Climate:     Operating: %s, Mode: %s, Rem: %d min, Stop: %s\n",
                                  cl.isOperating() ? "YES" : "NO", cl.getModeString().c_str(),
                                  cl.getRemainingMinutes(), cl.getStopTime().c_str());
        }
    } else if (cmd == "logs") {
        m_activeClient.println("--- LIVE LOG BUFFER DUMP ---");
        String logs;
        Logger::getLogs(logs, 4096);
        m_activeClient.print(logs);
        m_activeClient.println("--- END OF LOGS ---");
    } else if (cmd == "clearlogs") {
        Logger::clearLogs();
        m_activeClient.println("Log buffer cleared.");
    } else if (cmd.startsWith("climate heat")) {
        int dur = 10;
        if (cmd.length() > 13) dur = cmd.substring(13).toInt();
        if (dur != 10 && dur != 20 && dur != 30) dur = 10;
        m_phev->sendPreACCommand(PRE_AC_HEAT, dur, 0);
        m_activeClient.printf("Pre-heating requested for %d minutes.\n", dur);
    } else if (cmd.startsWith("climate cool")) {
        int dur = 10;
        if (cmd.length() > 13) dur = cmd.substring(13).toInt();
        if (dur != 10 && dur != 20 && dur != 30) dur = 10;
        m_phev->sendPreACCommand(PRE_AC_COOL, dur, 0);
        m_activeClient.printf("Pre-cooling requested for %d minutes.\n", dur);
    } else if (cmd == "climate stop") {
        m_phev->sendPreACStop();
        m_activeClient.println("Pre-AC stop requested.");
    } else if (cmd == "doors lock") {
        m_phev->sendDoorLock(true);
        m_activeClient.println("Door lock requested.");
    } else if (cmd == "doors unlock") {
        m_phev->sendDoorLock(false);
        m_activeClient.println("Door unlock requested.");
    } else if (cmd == "locate") {
        m_phev->sendHornHazard(true, true);
        m_activeClient.println("Locate horn/hazard triggered.");
    } else if (cmd == "reboot") {
        m_activeClient.println("Rebooting ESP32-S3 in 1 second. Goodbye!");
        m_activeClient.stop();
        delay(1000);
        ESP.restart();
    } else if (cmd == "sleep") {
        m_activeClient.println("Entering duty-cycle sleep now.");
        m_activeClient.stop();
        if (m_pwr) m_pwr->enterDutyCycleSleep();
    } else if (cmd.startsWith("ota ")) {
        String url = cmd.substring(4);
        url.trim();
        m_activeClient.printf("Starting Remote OTA update from: %s\n", url.c_str());
        bool ok = OTAManager::performOTA(url.c_str());
        if (ok) {
            m_activeClient.println("OTA Successful! Rebooting...");
            delay(1000);
            ESP.restart();
        } else {
            m_activeClient.println("OTA Failed! Check logs.");
        }
    } else if (cmd == "obd") {
        if (!m_obd) {
            m_activeClient.println("OBD2 service is not enabled.");
        } else {
            m_activeClient.println("=================================================");
            m_activeClient.println("  Mitsubishi Outlander PHEV OBD2 / BMU Telemetry ");
            m_activeClient.println("=================================================");
            m_activeClient.printf("Bluetooth Link:   %s\n", m_obd->isConnected() ? "CONNECTED" : "DISCONNECTED");
            m_activeClient.printf("ECU Diagnostics:  %s\n", m_obd->isEcuResponding() ? "ACTIVE (Awake)" : "NO DATA (Car Asleep / Off)");
            
            ObdTelemetry t = m_obd->getTelemetry();
            if (t.lastUpdate > 0) {
                m_activeClient.printf("Last Sample:      %u seconds ago\n", (millis() - t.lastUpdate) / 1000);
            }
            m_activeClient.printf("State of Health:  %.1f %%\n", t.soh);
            m_activeClient.printf("Battery Capacity: %.1f Ah (Remaining: %.1f Ah)\n", t.capacityAh, t.remainingAh);
            m_activeClient.printf("Cell Voltages(80):Min: %u mV, Max: %u mV, Delta: %u mV, Avg: %u mV\n",
                                  t.cellVoltageMin, t.cellVoltageMax, t.cellVoltageDelta, t.cellVoltageAvg);
            m_activeClient.printf("Pack Temperature: Min: %d °C, Max: %d °C, Avg: %d °C\n",
                                  t.tempMin, t.tempMax, t.tempAvg);
            m_activeClient.printf("HV Traction Pack: %.1f V, %.1f A (%.2f kW)\n",
                                  t.packVoltage, t.packCurrent, t.packPowerKw);
            m_activeClient.printf("Fuel Level:       %.1f Liters\n", t.fuelRemainingLiters);
            m_activeClient.printf("Engine Coolant:   %d °C\n", t.coolantTemp);
            m_activeClient.printf("Odometer:         %u km\n", t.odometer);
            m_activeClient.printf("Motors (F / R):   %.1f kW / %.1f kW\n", t.frontMotorKw, t.rearMotorKw);
            m_activeClient.println("=================================================");
        }
    } else if (cmd == "setup") {
        ConfigManager::startSetupPortal("PHEV-Bridge-Setup");
        m_activeClient.println("Captive Portal Hotspot 'PHEV-Bridge-Setup' started!");
        m_activeClient.println("Connect to the Wi-Fi with your phone and browse to http://192.168.4.1");
    } else if (cmd == "config") {
        AppConfig c = ConfigManager::current();
        m_activeClient.println("--- STORED CONFIGURATION ---");
        m_activeClient.printf("Configured:     %s\n", c.configured ? "YES" : "NO (Defaults)");
        m_activeClient.printf("Vehicle SSID:   %s\n", c.wifi_ssid.c_str());
        m_activeClient.printf("Max SOC Scale:  %d %%\n", c.battery_max_soc);
        m_activeClient.printf("Cellular APN:   %s (SIM PIN: %s)\n", c.lte_apn.c_str(), c.sim_pin.length() > 0 ? "****" : "None");
        m_activeClient.printf("WireGuard VPN:  %s (Host: %s:%d, IP: %s)\n",
                              c.wg_enabled ? "Enabled" : "Disabled",
                              c.wg_endpoint_host.c_str(), c.wg_endpoint_port, c.wg_local_ip.c_str());
        m_activeClient.printf("MQTT Broker:    %s:%d (User: %s, Prefix: %s)\n",
                              c.mqtt_server.c_str(), c.mqtt_port, c.mqtt_user.c_str(), c.mqtt_prefix.c_str());
        m_activeClient.printf("OBD2 Bluetooth: %s (Device: %s, PIN: %s, Nom: %.1f Ah)\n",
                              c.obd_enabled ? "Enabled" : "Disabled",
                              c.obd_device_name.c_str(), c.obd_pin.c_str(), c.obd_nominal_ah);
        m_activeClient.println("----------------------------");
    } else if (cmd == "factoryreset") {
        m_activeClient.println("Erasing all configuration from NVS flash...");
        ConfigManager::reset();
        m_activeClient.println("Configuration erased. Rebooting in 1s into Setup Hotspot mode...");
        delay(1000);
        ESP.restart();
    } else if (cmd == "exit" || cmd == "quit") {
        m_activeClient.println("Session closed.");
        m_activeClient.stop();
    } else {
        m_activeClient.printf("Unknown command: '%s'. Type 'help' for command list.\n", cmd.c_str());
    }
}
