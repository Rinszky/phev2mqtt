#ifndef REMOTE_CLI_H
#define REMOTE_CLI_H

#include <Arduino.h>
#include <WiFiServer.h>
#include <WiFiClient.h>
#include "phev_client.h"
#include "cellular_modem.h"
#include "power_manager.h"

class RemoteCLI {
public:
    RemoteCLI();

    void begin(uint16_t port, const char* password,
               PhevClient* phev, CellularModem* modem, PowerManager* pwr);
    void loop();

private:
    WiFiServer m_server;
    WiFiClient m_activeClient;
    String m_password;
    bool m_authenticated;
    PhevClient* m_phev;
    CellularModem* m_modem;
    PowerManager* m_pwr;
    String m_cmdBuffer;

    void handleClient();
    void executeCommand(const String& cmd);
    void printPrompt();
};

#endif // REMOTE_CLI_H
