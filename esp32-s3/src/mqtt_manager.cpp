#include "mqtt_manager.h"
#include "logger.h"
#include <ArduinoJson.h>

#define TAG "MQTT"

static MqttManager* s_instance = nullptr;

MqttManager::MqttManager()
    : m_port(1883),
      m_haDiscovery(true),
      m_haPublished(false),
      m_phev(nullptr),
      m_lastConnectAttempt(0),
      m_lastStatePublish(0) {
    s_instance = this;
}

String MqttManager::topic(const String& subTopic) {
    String t = m_prefix;
    if (subTopic.length() > 0 && subTopic[0] != '/') t += "/";
    t += subTopic;
    return t;
}

String MqttManager::topic(const char* subTopic) {
    return topic(String(subTopic));
}

bool MqttManager::begin(Client& netClient,
                        const char* server,
                        uint16_t port,
                        const char* user,
                        const char* pass,
                        const char* prefix,
                        PhevClient* phevClient) {
    m_server = server;
    m_port = port;
    m_user = user;
    m_pass = pass;
    m_prefix = prefix;
    m_haPrefix = "homeassistant";
    m_phev = phevClient;

    m_mqtt.setClient(netClient);
    m_mqtt.setServer(server, port);
    m_mqtt.setCallback(callbackStatic);
    m_mqtt.setBufferSize(2048); // Large buffer for HA discovery payloads

    LOG_I(TAG, "Configured MQTT to %s:%d, prefix=%s", server, port, prefix);
    return true;
}

void MqttManager::callbackStatic(char* topic, uint8_t* payload, unsigned int length) {
    if (s_instance) {
        s_instance->handleMessage(topic, payload, length);
    }
}

bool MqttManager::isConnected() {
    return m_mqtt.connected();
}

void MqttManager::subscribeCommands() {
    String subPattern = topic("/#");
    m_mqtt.subscribe(topic("/climate/+/set").c_str());
    m_mqtt.subscribe(topic("/door/lock/set").c_str());
    m_mqtt.subscribe(topic("/lights/+/set").c_str());
    m_mqtt.subscribe(topic("/locate/set").c_str());
    m_mqtt.subscribe(topic("/system/+/set").c_str());
    m_mqtt.subscribe("homeassistant/status"); // Listen for HA restart
    LOG_I(TAG, "Subscribed to MQTT command topics");
}

void MqttManager::loop() {
    if (!m_mqtt.connected()) {
        uint32_t now = millis();
        if (now - m_lastConnectAttempt > 5000) {
            m_lastConnectAttempt = now;
            String clientId = "ESP32S3-PHEV-" + String((uint32_t)ESP.getEfuseMac(), HEX);
            String willTopic = topic("/status");

            LOG_I(TAG, "Connecting to MQTT broker (%s:%d)...", m_server.c_str(), m_port);
            if (m_mqtt.connect(clientId.c_str(), m_user.c_str(), m_pass.c_str(), willTopic.c_str(), 1, true, "offline")) {
                LOG_I(TAG, "MQTT connected successfully!");
                publish("/status", "online", true);
                subscribeCommands();
                if (m_haDiscovery && !m_haPublished) {
                    publishDiscovery();
                }
                publishAllStates();
            } else {
                LOG_W(TAG, "MQTT connect failed, rc=%d", m_mqtt.state());
            }
        }
        return;
    }

    m_mqtt.loop();

    uint32_t now = millis();
    if (now - m_lastStatePublish > 10000) {
        m_lastStatePublish = now;
        publishAllStates();
    }
}

void MqttManager::publish(const char* subTopic, const char* payload, bool retain) {
    if (!m_mqtt.connected()) return;
    String t = topic(subTopic);
    m_mqtt.publish(t.c_str(), payload, retain);
}

void MqttManager::publish(const char* subTopic, int value, bool retain) {
    char buf[16];
    snprintf(buf, sizeof(buf), "%d", value);
    publish(subTopic, buf, retain);
}

void MqttManager::publishAllStates() {
    if (!m_phev || !m_mqtt.connected()) return;

    // 1. Battery
    const BatteryStatus& bat = m_phev->getBatteryStatus();
    publish("/battery/level", bat.scaledLevel);
    publish("/battery/raw_level", bat.rawLevel);
    publish("/lights/parking", bat.parkingLights ? "on" : "off");

    // 2. Charging
    const ChargingStatus& chg = m_phev->getChargingStatus();
    publish("/battery/charging", chg.charging ? "on" : "off");
    publish("/battery/plugged", chg.plugged ? "on" : "off");
    publish("/battery/charge_time_remaining", (int)chg.remainingMinutes);
    publish("/battery/charge_completion_time", chg.completionTime.c_str());

    // 3. Doors & Lights
    const VehicleDoors& d = m_phev->getDoors();
    publish("/door/locked", d.locked ? "locked" : "unlocked");
    publish("/door/driver", d.driverOpen ? "open" : "closed");
    publish("/door/passenger", d.passengerOpen ? "open" : "closed");
    publish("/door/rear_right", d.rearRightOpen ? "open" : "closed");
    publish("/door/rear_left", d.rearLeftOpen ? "open" : "closed");
    publish("/door/boot", d.bootOpen ? "open" : "closed");
    publish("/door/bonnet", d.bonnetOpen ? "open" : "closed");
    publish("/lights/head", d.headlights ? "on" : "off");

    // 4. Climate
    ClimateTracker& cl = m_phev->getClimateTracker();
    cl.updateTiming(time(nullptr));
    publish("/climate/heat", cl.isHeating() ? "on" : "off");
    publish("/climate/cool", cl.isCooling() ? "on" : "off");
    publish("/climate/windscreen", cl.isWindscreen() ? "on" : "off");
    publish("/climate/mode", cl.getModeString().c_str());
    publish("/climate/state", cl.getModeString().c_str());
    publish("/climate/operating", cl.isOperating() ? "on" : "off");
    publish("/climate/remaining", cl.getRemainingMinutes());
    publish("/climate/start_time", cl.getStartTime().c_str());
    publish("/climate/stop_time", cl.getStopTime().c_str());
    publish("/climate/completion_time", cl.getCompletionTime().c_str());
    publish("/climate/duration", (int)cl.getSelectedDuration());
    publish("/climate/delay", (int)cl.getSelectedDelay());

    // 5. System
    publish("/system/wifi_car", m_phev->isCarConnected() ? "connected" : "disconnected");
    publish("/system/vin", m_phev->getVIN().c_str());
    publish("/system/ecu_version", m_phev->getECUVersion().c_str());
    publish("/system/uptime", (int)(millis() / 1000));
}

void MqttManager::handleMessage(char* topicStr, uint8_t* payload, unsigned int length) {
    char payloadStr[length + 1];
    memcpy(payloadStr, payload, length);
    payloadStr[length] = '\0';
    String p = String(payloadStr);
    p.trim();
    String t = String(topicStr);

    LOG_I(TAG, "MQTT Rx: %s -> %s", t.c_str(), p.c_str());

    // Home Assistant rebirth
    if (t == "homeassistant/status" && p.equalsIgnoreCase("online")) {
        LOG_I(TAG, "Home Assistant online received! Republishing discovery");
        publishDiscovery();
        publishAllStates();
        return;
    }

    if (!m_phev) return;

    // Climate Commands
    if (t == topic("/climate/heat/set")) {
        if (p.equalsIgnoreCase("on") || p == "1" || p.equalsIgnoreCase("true")) {
            m_phev->sendPreACCommand(PRE_AC_HEAT, m_phev->getClimateTracker().getSelectedDuration(), m_phev->getClimateTracker().getSelectedDelay());
        } else {
            m_phev->sendPreACStop();
        }
    } else if (t == topic("/climate/cool/set")) {
        if (p.equalsIgnoreCase("on") || p == "1" || p.equalsIgnoreCase("true")) {
            m_phev->sendPreACCommand(PRE_AC_COOL, m_phev->getClimateTracker().getSelectedDuration(), m_phev->getClimateTracker().getSelectedDelay());
        } else {
            m_phev->sendPreACStop();
        }
    } else if (t == topic("/climate/windscreen/set")) {
        if (p.equalsIgnoreCase("on") || p == "1" || p.equalsIgnoreCase("true")) {
            m_phev->sendPreACCommand(PRE_AC_WINDSCREEN, m_phev->getClimateTracker().getSelectedDuration(), m_phev->getClimateTracker().getSelectedDelay());
        } else {
            m_phev->sendPreACStop();
        }
    } else if (t == topic("/climate/duration/set")) {
        int dur = p.toInt();
        if (dur == 10 || dur == 20 || dur == 30) {
            m_phev->getClimateTracker().setModeAndDuration(m_phev->getClimateStatus().mode, dur);
            publish("/climate/duration", dur);
        }
    } else if (t == topic("/climate/delay/set")) {
        int del = p.toInt();
        if (del == 0 || del == 5 || del == 10) {
            m_phev->getClimateTracker().setACOperStatus(m_phev->getClimateTracker().isOperating(), m_phev->getClimateTracker().getSelectedDuration(), del, del > 0);
            publish("/climate/delay", del);
        }
    }
    // Door Lock Commands
    else if (t == topic("/door/lock/set")) {
        bool lock = p.equalsIgnoreCase("lock") || p.equalsIgnoreCase("on") || p == "1";
        m_phev->sendDoorLock(lock);
    }
    // Light Commands
    else if (t == topic("/lights/head/set")) {
        bool on = p.equalsIgnoreCase("on") || p == "1";
        m_phev->sendHeadlights(on);
    } else if (t == topic("/lights/parking/set")) {
        bool on = p.equalsIgnoreCase("on") || p == "1";
        m_phev->sendParkingLights(on);
    }
    // Locate (Horn/Hazard)
    else if (t == topic("/locate/set")) {
        m_phev->sendHornHazard(true, true);
    }
    // System Commands
    else if (t == topic("/system/reboot/set")) {
        LOG_W(TAG, "Remote reboot requested via MQTT! Restarting in 1s...");
        delay(1000);
        ESP.restart();
    }
}

void MqttManager::publishDiscovery() {
    LOG_I(TAG, "Publishing Home Assistant MQTT auto-discovery configs...");
    String vin = m_phev ? m_phev->getVIN() : "PHEV";
    if (vin.length() == 0) vin = "PHEV";

    JsonDocument doc;
    JsonObject dev = doc["device"].to<JsonObject>();
    dev["identifiers"][0] = vin;
    dev["name"] = "Mitsubishi Outlander PHEV";
    dev["manufacturer"] = "Mitsubishi Motors";
    dev["model"] = "Outlander PHEV (ESP32-S3 Bridge)";

    auto publishSensor = [&](const char* type, const char* entity, const char* name, const char* devClass, const char* unit, const char* icon) {
        doc.clear();
        doc["name"] = name;
        doc["unique_id"] = vin + "_" + entity;
        doc["state_topic"] = topic(String("/") + entity);
        if (devClass) doc["device_class"] = devClass;
        if (unit) doc["unit_of_measurement"] = unit;
        if (icon) doc["icon"] = icon;
        doc["device"] = dev;

        String configTopic = m_haPrefix + "/" + type + "/" + vin + "/" + entity + "/config";
        String payload;
        serializeJson(doc, payload);
        m_mqtt.publish(configTopic.c_str(), payload.c_str(), true);
    };

    // Sensors
    publishSensor("sensor", "battery/level", "Battery Level", "battery", "%", "mdi:battery");
    publishSensor("sensor", "battery/raw_level", "Battery Raw Level", "battery", "%", "mdi:battery-alert");
    publishSensor("sensor", "battery/charge_time_remaining", "Charge Time Remaining", "duration", "min", "mdi:timer-outline");
    publishSensor("sensor", "battery/charge_completion_time", "Charge Completion Time", "timestamp", nullptr, "mdi:clock-check-outline");
    publishSensor("sensor", "climate/remaining", "Climate Remaining Time", "duration", "min", "mdi:timer-sand");
    publishSensor("sensor", "climate/stop_time", "Climate Stop Time", nullptr, nullptr, "mdi:clock-end");
    publishSensor("sensor", "climate/start_time", "Climate Start Time", nullptr, nullptr, "mdi:clock-start");
    publishSensor("sensor", "climate/completion_time", "Climate Completion Time", "timestamp", nullptr, "mdi:clock-check");
    publishSensor("sensor", "system/12v_voltage", "12V Battery Voltage", "voltage", "V", "mdi:car-battery");

    // Binary Sensors
    publishSensor("binary_sensor", "battery/charging", "Battery Charging", "battery_charging", nullptr, nullptr);
    publishSensor("binary_sensor", "battery/plugged", "Charge Plug Connected", "plug", nullptr, nullptr);
    publishSensor("binary_sensor", "climate/operating", "Climate Active", "power", nullptr, "mdi:hvac");
    publishSensor("binary_sensor", "door/driver", "Driver Door", "door", nullptr, nullptr);
    publishSensor("binary_sensor", "door/passenger", "Passenger Door", "door", nullptr, nullptr);
    publishSensor("binary_sensor", "door/boot", "Boot", "door", nullptr, nullptr);
    publishSensor("binary_sensor", "door/bonnet", "Bonnet", "door", nullptr, nullptr);

    // Switches & Controls
    auto publishSwitch = [&](const char* entity, const char* name, const char* icon) {
        doc.clear();
        doc["name"] = name;
        doc["unique_id"] = vin + "_" + entity;
        doc["state_topic"] = topic(String("/") + entity);
        doc["command_topic"] = topic(String("/") + entity + "/set");
        if (icon) doc["icon"] = icon;
        doc["device"] = dev;

        String configTopic = m_haPrefix + "/switch/" + vin + "/" + entity + "/config";
        String payload;
        serializeJson(doc, payload);
        m_mqtt.publish(configTopic.c_str(), payload.c_str(), true);
    };

    publishSwitch("climate/heat", "Pre-Heating", "mdi:radiator");
    publishSwitch("climate/cool", "Pre-Cooling", "mdi:air-conditioner");
    publishSwitch("climate/windscreen", "Windscreen Defrost", "mdi:weather-windy");
    publishSwitch("lights/head", "Headlights", "mdi:car-light-high");
    publishSwitch("lights/parking", "Parking Lights", "mdi:car-parking-lights");

    m_haPublished = true;
    LOG_I(TAG, "Home Assistant discovery configs published successfully!");
}
