#include "remote_cli.h"
#include "logger.h"
#include "ota_manager.h"

#define TAG "CLI"

RemoteCLI::RemoteCLI()
    : m_server(2222),
      m_authenticated(false),
      m_phev(nullptr),
      m_modem(nullptr),
      m_pwr(nullptr) {
}

void RemoteCLI::begin(uint16_t port, const char* password,
                      PhevClient* phev, CellularModem* modem, PowerManager* pwr) {
    m_password = password;
    m_phev = phev;
    m_modem = modem;
    m_pwr = pwr;
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
    } else if (cmd == "exit" || cmd == "quit") {
        m_activeClient.println("Session closed.");
        m_activeClient.stop();
    } else {
        m_activeClient.printf("Unknown command: '%s'. Type 'help' for command list.\n", cmd.c_str());
    }
}
