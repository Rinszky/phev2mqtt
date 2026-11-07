#!/usr/bin/with-contenv bashio

if bashio::config.true 'debug'; then
    bashio::log.info "Debug mode enabled - sleeping indefinitely"
    sleep infinity
fi

CAR_ADDRESS="$(bashio::config 'car_address')"
if [[ -z "${CAR_ADDRESS}" || "${CAR_ADDRESS}" == "null" ]]; then
    CAR_ADDRESS="192.168.8.46:8080"
fi

MQTT_SERVER="$(bashio::config 'mqtt_server')"
MQTT_USER="$(bashio::config 'mqtt_user')"
MQTT_PASSWORD="$(bashio::config 'mqtt_password')"

if [[ -z "${MQTT_SERVER}" || "${MQTT_SERVER}" == "null" ]]; then
    if bashio::services.available "mqtt"; then
        MQTT_HOST="$(bashio::services mqtt 'host')"
        MQTT_PORT="$(bashio::services mqtt 'port')"
        MQTT_SERVER="tcp://${MQTT_HOST}:${MQTT_PORT}"
        if [[ -z "${MQTT_USER}" || "${MQTT_USER}" == "null" ]]; then
            MQTT_USER="$(bashio::services mqtt 'username')"
            MQTT_PASSWORD="$(bashio::services mqtt 'password')"
        fi
        bashio::log.info "Auto-detected Home Assistant MQTT service at ${MQTT_SERVER}"
    else
        MQTT_SERVER="tcp://127.0.0.1:1883"
    fi
elif [[ "${MQTT_SERVER}" != tcp://* && "${MQTT_SERVER}" != ssl://* && "${MQTT_SERVER}" != ws://* ]]; then
    MQTT_SERVER="tcp://${MQTT_SERVER}"
fi

TOPIC_PREFIX="$(bashio::config 'mqtt_topic_prefix')"
if [[ -z "${TOPIC_PREFIX}" || "${TOPIC_PREFIX}" == "null" ]]; then
    TOPIC_PREFIX="phev"
fi

UPDATE_INTERVAL="$(bashio::config 'update_interval')"
if [[ -z "${UPDATE_INTERVAL}" || "${UPDATE_INTERVAL}" == "null" ]]; then
    UPDATE_INTERVAL="5m"
fi

VERBOSITY="$(bashio::config 'verbosity')"
if [[ -z "${VERBOSITY}" || "${VERBOSITY}" == "null" ]]; then
    VERBOSITY="info"
fi

ARGS=(
    "-v" "${VERBOSITY}"
    "client"
    "--address" "${CAR_ADDRESS}"
    "mqtt"
    "--mqtt_server" "${MQTT_SERVER}"
    "--mqtt_topic_prefix" "${TOPIC_PREFIX}"
    "--update_interval" "${UPDATE_INTERVAL}"
    "--wifi_restart_command" ""
)

if [[ -n "${MQTT_USER}" && "${MQTT_USER}" != "null" ]]; then
    ARGS+=("--mqtt_username" "${MQTT_USER}")
fi
if [[ -n "${MQTT_PASSWORD}" && "${MQTT_PASSWORD}" != "null" ]]; then
    ARGS+=("--mqtt_password" "${MQTT_PASSWORD}")
fi
if bashio::config.false 'ha_discovery'; then
    ARGS+=("--ha_discovery=false")
fi
if bashio::config.true 'ha_keep_sensors_available'; then
    ARGS+=("--ha_keep_sensors_available=true")
fi
if bashio::config.true 'auto_sync_time'; then
    ARGS+=("--auto_sync_time=true")
fi

bashio::log.info "Starting phev2mqtt (car=${CAR_ADDRESS}, broker=${MQTT_SERVER}, prefix=${TOPIC_PREFIX})"
exec /opt/phev2mqtt "${ARGS[@]}"
