#ifndef MQTT_MANAGER_H
#define MQTT_MANAGER_H

#include <Arduino.h>
#include <PubSubClient.h>
#include <Client.h>
#include "phev_client.h"
#include "cellular_modem.h"
#include "obd_reader.h"
#include "wss_mqtt_client.h"

class MqttManager {
public:
    MqttManager();

    bool begin(Client& netClient,
               const char* server,
               uint16_t port,
               const char* user,
               const char* pass,
               const char* prefix,
               const char* websocketPath,
               PhevClient* phevClient,
               CellularModem* modem = nullptr,
               ObdReader* obdReader = nullptr);
    void loop();

    bool isConnected();
    void publishDiscovery();
    void publishAllStates();
    void publish(const char* subTopic, const char* payload, bool retain = true);
    void publish(const char* subTopic, int value, bool retain = true);

    void setHAEnabled(bool en) { m_haDiscovery = en; }
    void setHAPrefix(const char* p) { m_haPrefix = p; }

private:
    WssMqttClient m_wssClient;
    PubSubClient m_mqtt;
    String m_server;
    uint16_t m_port;
    String m_user;
    String m_pass;
    String m_prefix;
    String m_haPrefix;
    bool m_haDiscovery;
    bool m_haPublished;
    PhevClient* m_phev;
    CellularModem* m_modem;
    ObdReader* m_obd;
    uint32_t m_lastConnectAttempt;
    uint32_t m_lastStatePublish;

    String topic(const String& subTopic);
    String topic(const char* subTopic);
    void subscribeCommands();
    void handleMessage(char* topic, uint8_t* payload, unsigned int length);
    static void callbackStatic(char* topic, uint8_t* payload, unsigned int length);
};

#endif // MQTT_MANAGER_H
