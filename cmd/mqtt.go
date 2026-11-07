/*
Copyright © 2021 Ben Buxton <bbuxton@gmail.com>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const defaultWifiRestartCmd = "sudo ip link set wlan0 down && sleep 3 && sudo ip link set wlan0 up"

// mqttCmd represents the mqtt command
var mqttCmd = &cobra.Command{
	Use:   "mqtt",
	Short: "Start an MQTT bridge.",
	Long: `Maintains a connection to the PHEV (retrying as needed) and to an MQTT server.

Status data, climate/charge schedules, and vehicle customization settings from the car
are published to MQTT topics (with Home Assistant MQTT Auto-Discovery), and commands from
MQTT can control climate, timers, lights, clock sync, and vehicle settings.
`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		mc := newMqttClient()
		return mc.Run(cmd, args)
	},
}

// climate tracks complete immediate Pre-AC state across registers 0x10, 0x1a, and 0x1c.
type climate struct {
	state            *protocol.PreACState
	mode             *string
	operating        bool
	delayPending     bool
	selectedDuration int // 10, 20, 30
	selectedDelay    int // 0, 5, 10
	stopTime         string
}

func newClimateTracker() *climate {
	return &climate{
		selectedDuration: 10,
		selectedDelay:    0,
		stopTime:         "--:--",
	}
}

func (c *climate) setMode(m string, dur uint8) {
	c.mode = &m
	if dur == 10 || dur == 20 || dur == 30 {
		c.selectedDuration = int(dur)
	}
}

func (c *climate) setState(reg *protocol.RegisterPreACState) {
	st := reg.State
	c.state = &st
	c.stopTime = reg.TargetTimeString()
}

func (c *climate) setOper(reg *protocol.RegisterACOperStatus) {
	c.operating = reg.Operating
	c.delayPending = reg.DelayPending
	if reg.Duration == 10 || reg.Duration == 20 || reg.Duration == 30 {
		c.selectedDuration = reg.Duration
	}
	if reg.Delay == 0 || reg.Delay == 5 || reg.Delay == 10 {
		c.selectedDelay = reg.Delay
	}
}

func (c *climate) mqttStates() map[string]string {
	m := map[string]string{
		"/climate/state":      "off",
		"/climate/mode":       "off",
		"/climate/cool":       "off",
		"/climate/heat":       "off",
		"/climate/windscreen": "off",
		"/climate/operating":  boolOnOff[c.operating],
		"/climate/duration":   strconv.Itoa(c.selectedDuration),
		"/climate/delay":      strconv.Itoa(c.selectedDelay),
		"/climate/stop_time":  c.stopTime,
	}
	if c.state == nil {
		return m
	}
	activeMode := "heat"
	if c.mode != nil && *c.mode != "" && *c.mode != "unknown" {
		activeMode = *c.mode
	}

	switch *c.state {
	case protocol.PreACOff:
		if c.delayPending {
			m["/climate/state"] = "delayed"
			m["/climate/mode"] = activeMode
		} else {
			m["/climate/state"] = "off"
			m["/climate/mode"] = "off"
		}
		return m
	case protocol.PreACStarting:
		if c.delayPending {
			m["/climate/state"] = "delayed"
		} else {
			m["/climate/state"] = "starting"
		}
		m["/climate/mode"] = activeMode
	case protocol.PreACOn:
		m["/climate/state"] = activeMode
		m["/climate/mode"] = activeMode
	case protocol.PreACTerminated:
		m["/climate/state"] = "terminated"
		m["/climate/mode"] = "off"
		return m
	default:
		m["/climate/state"] = "unknown"
		return m
	}

	switch activeMode {
	case "cool":
		m["/climate/cool"] = "on"
	case "heat":
		m["/climate/heat"] = "on"
	case "windscreen":
		m["/climate/windscreen"] = "on"
	}
	return m
}

var lastWifiRestart time.Time

func restartWifi() error {
	restartRetryTime := viper.GetDuration("wifi_restart_retry_time")

	if time.Since(lastWifiRestart) < restartRetryTime {
		return nil
	}
	defer func() {
		lastWifiRestart = time.Now()
	}()

	restartCommand := viper.GetString("wifi_restart_command")
	if restartCommand == "" {
		log.Debugf("wifi restart disabled")
		return nil
	}

	log.Debugf("Attempting to restart wifi")
	restartCmd := exec.Command("/bin/sh", "-c", restartCommand)
	stdoutStderr, err := restartCmd.CombinedOutput()
	if len(stdoutStderr) > 0 {
		log.Infof("Output from wifi restart: %s", stdoutStderr)
	}
	return err
}

type mqttClient struct {
	mu             sync.RWMutex
	client         mqtt.Client
	options        *mqtt.ClientOptions
	mqttData       map[string]string
	updateInterval time.Duration

	phev          *client.Client
	carConnected  bool
	lastConnect   time.Time
	lastError     error
	lastTimeSync  time.Time
	pollInFlight  int32
	locateRunning int32

	prefix               string
	mqttDisableSet       bool
	mqttRetain           bool
	autoSyncTime         bool
	haDiscovery          bool
	haDiscoveryPrefix    string
	haKeepSensorsOnline  bool
	haPublishedDiscovery bool

	vin                string
	fullVIN            string
	ecuVersion         string
	steeringSide       string
	v2hSupported       bool
	discoveredV2H      bool
	discoveredSettings map[string]bool

	climate         *climate
	climateSchedule protocol.ClimateSchedule
	chargeSchedule  protocol.ChargeSchedule
	settings        *protocol.Settings
	my14Reg22       []byte
	my14Reg19       []byte

	enabled bool
}

func newMqttClient() *mqttClient {
	return &mqttClient{
		mqttData:           make(map[string]string),
		discoveredSettings: make(map[string]bool),
		climate:            newClimateTracker(),
		climateSchedule:    protocol.NewDefaultClimateSchedule(),
		chargeSchedule:     protocol.NewDefaultChargeSchedule(),
		settings:           &protocol.Settings{},
		enabled:            true,
	}
}

func (m *mqttClient) topic(sub string) string {
	return fmt.Sprintf("%s%s", m.prefix, sub)
}

func (m *mqttClient) getPhev() *client.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.phev
}

func (m *mqttClient) setPhev(cl *client.Client, connected bool) {
	m.mu.Lock()
	m.phev = cl
	m.carConnected = connected
	m.mu.Unlock()
}

func (m *mqttClient) clearPhevIfMatch(cl *client.Client) {
	m.mu.Lock()
	if m.phev == cl {
		m.phev = nil
		m.carConnected = false
	}
	m.mu.Unlock()
}

func (m *mqttClient) isEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

func (m *mqttClient) setEnabled(v bool) {
	m.mu.Lock()
	m.enabled = v
	m.mu.Unlock()
}

func (m *mqttClient) Run(cmd *cobra.Command, args []string) error {
	mqttServer := viper.GetString("mqtt_server")
	mqttUsername := viper.GetString("mqtt_username")
	mqttPassword := viper.GetString("mqtt_password")
	m.mqttDisableSet = viper.GetBool("mqtt_disable_register_set_command")
	m.mqttRetain = viper.GetBool("mqtt_retain")
	m.autoSyncTime = viper.GetBool("auto_sync_time")
	m.prefix = strings.TrimRight(viper.GetString("mqtt_topic_prefix"), "/")
	m.haDiscovery = viper.GetBool("ha_discovery")
	m.haDiscoveryPrefix = strings.TrimRight(viper.GetString("ha_discovery_prefix"), "/")
	m.haKeepSensorsOnline = viper.GetBool("ha_keep_sensors_available")
	m.updateInterval = viper.GetDuration("update_interval")
	wifiRestartTime := viper.GetDuration("wifi_restart_time")
	restartCommand := viper.GetString("wifi_restart_command")

	if restartCommand == "" {
		log.Infof("WiFi restart disabled")
	}

	m.options = mqtt.NewClientOptions().
		AddBroker(mqttServer).
		SetClientID("phev2mqtt").
		SetUsername(mqttUsername).
		SetPassword(mqttPassword).
		SetAutoReconnect(true).
		SetDefaultPublishHandler(m.handleIncomingMqtt).
		SetOnConnectHandler(m.onMqttConnect).
		SetWill(m.topic("/bridge/available"), "offline", 0, true)

	m.client = mqtt.NewClient(m.options)
	if token := m.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	for {
		if m.isEnabled() {
			if err := m.handlePhev(); err != nil {
				if m.lastError == nil || m.lastError.Error() != err.Error() {
					log.Error(err)
					m.lastError = err
				}
			}
			m.mu.RLock()
			sinceLast := time.Since(m.lastConnect)
			m.mu.RUnlock()

			if sinceLast > 30*time.Second {
				m.client.Publish(m.topic("/available"), 0, true, "offline")
			}
			if wifiRestartTime > 0 && sinceLast > wifiRestartTime {
				if err := restartWifi(); err != nil {
					log.Errorf("Error restarting wifi: %v", err)
				}
			}
		}

		time.Sleep(time.Second)
	}
}

func (m *mqttClient) onMqttConnect(c mqtt.Client) {
	log.Infof("Connected to MQTT broker")
	c.Publish(m.topic("/bridge/available"), 0, true, "online")

	m.mu.RLock()
	carConn := m.carConnected
	m.mu.RUnlock()
	if carConn {
		c.Publish(m.topic("/available"), 0, true, "online")
	} else {
		c.Publish(m.topic("/available"), 0, true, "offline")
	}

	if !m.mqttDisableSet {
		if token := c.Subscribe(m.topic("/set/#"), 0, nil); token.Wait() && token.Error() != nil {
			log.Errorf("Error subscribing to /set/#: %v", token.Error())
		}
	} else {
		log.Info("Setting vehicle registers via MQTT is disabled")
	}
	if token := c.Subscribe(m.topic("/connection"), 0, nil); token.Wait() && token.Error() != nil {
		log.Errorf("Error subscribing to /connection: %v", token.Error())
	}
	if token := c.Subscribe(m.topic("/settings/dump"), 0, nil); token.Wait() && token.Error() != nil {
		log.Errorf("Error subscribing to /settings/dump: %v", token.Error())
	}
	if m.haDiscovery && m.haDiscoveryPrefix != "" {
		birthTopic := m.haDiscoveryPrefix + "/status"
		if token := c.Subscribe(birthTopic, 0, nil); token.Wait() && token.Error() != nil {
			log.Debugf("Could not subscribe to HA birth topic %s: %v", birthTopic, token.Error())
		}
	}

	m.republishAllDiscoveryAndState()
}

func (m *mqttClient) republishAllDiscoveryAndState() {
	m.mu.Lock()
	vin := m.vin
	if vin != "" {
		m.haPublishedDiscovery = false
		m.discoveredV2H = false
		m.discoveredSettings = make(map[string]bool)
	}
	cached := make(map[string]string, len(m.mqttData))
	for k, v := range m.mqttData {
		cached[k] = v
	}
	m.mu.Unlock()

	if vin != "" {
		m.publishHomeAssistantDiscovery(vin, m.prefix, "Phev")
	}
	for t, p := range cached {
		retain := m.mqttRetain && !strings.HasPrefix(t, "/register/")
		m.client.Publish(m.topic(t), 0, retain, p)
	}
}

func (m *mqttClient) publish(subTopic, payload string) {
	retain := m.mqttRetain && !strings.HasPrefix(subTopic, "/register/")
	m.mu.Lock()
	m.mqttData[subTopic] = payload
	m.mu.Unlock()
	m.client.Publish(m.topic(subTopic), 0, retain, payload)
}

func parseOnOffBool(payload string) (bool, bool) {
	switch strings.TrimSpace(strings.ToLower(payload)) {
	case "on", "true", "1", "enable", "enabled", "be":
		return true, true
	case "off", "false", "0", "disable", "disabled", "ki":
		return false, true
	default:
		return false, false
	}
}

func (m *mqttClient) handleIncomingMqtt(mqttClientConn mqtt.Client, msg mqtt.Message) {
	fullTopic := msg.Topic()
	payloadStr := strings.TrimSpace(string(msg.Payload()))

	// Handle Home Assistant birth message ("online") to re-announce discovery & cached states.
	if m.haDiscovery && fullTopic == m.haDiscoveryPrefix+"/status" {
		if strings.EqualFold(payloadStr, "online") {
			log.Infof("Home Assistant birth message received, re-publishing discovery and states")
			go m.republishAllDiscoveryAndState()
		}
		return
	}

	log.Infof("Topic: [%s] Payload: [%s]", fullTopic, payloadStr)

	if !strings.HasPrefix(fullTopic, m.prefix+"/") {
		return
	}
	relTopic := strings.TrimPrefix(fullTopic, m.prefix)

	if relTopic == "/connection" {
		switch strings.ToLower(payloadStr) {
		case "off":
			m.setEnabled(false)
			if cl := m.getPhev(); cl != nil {
				_ = cl.Close()
			}
			m.client.Publish(m.topic("/available"), 0, true, "offline")
		case "on":
			m.setEnabled(true)
		case "restart":
			m.setEnabled(true)
			m.client.Publish(m.topic("/available"), 0, true, "offline")
			if cl := m.getPhev(); cl != nil {
				_ = cl.Close()
			}
		}
		return
	}

	if relTopic == "/settings/dump" {
		if cl := m.getPhev(); cl != nil {
			log.Infof("CURRENT_SETTINGS:\n%s", cl.Settings.Dump())
		} else {
			log.Infof("CURRENT_SETTINGS (cached):\n%s", m.settings.Dump())
		}
		return
	}

	if m.mqttDisableSet {
		log.Warnf("Ignoring command on %s because mqtt_disable_register_set_command is enabled", fullTopic)
		return
	}

	cl := m.getPhev()
	if cl == nil || cl.IsClosed() {
		log.Warnf("Cannot execute command on %s: vehicle is not connected", fullTopic)
		return
	}

	switch {
	case strings.HasPrefix(relTopic, "/set/register/"):
		regHex := strings.TrimPrefix(relTopic, "/set/register/")
		register, err := hex.DecodeString(regHex)
		if err != nil || len(register) != 1 {
			log.Infof("Bad register in topic [%s]: %v", fullTopic, err)
			return
		}
		data, err := hex.DecodeString(payloadStr)
		if err != nil {
			log.Infof("Bad hex payload [%s]: %v", payloadStr, err)
			return
		}
		if err := cl.SetRegister(register[0], data); err != nil {
			log.Infof("Error setting register %02x: %v", register[0], err)
		}

	case relTopic == "/set/refresh":
		if err := cl.SetRegister(protocol.SetRequestUpdateRegister, []byte{0x03}); err != nil {
			log.Infof("Error requesting status update: %v", err)
		}

	case relTopic == "/set/synctime":
		targetTime := time.Now()
		if payloadStr != "" && !strings.EqualFold(payloadStr, "press") && !strings.EqualFold(payloadStr, "now") && !strings.EqualFold(payloadStr, "sync") {
			if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", payloadStr, time.Local); err == nil {
				targetTime = parsed
			} else if parsedISO, err2 := time.Parse(time.RFC3339, payloadStr); err2 == nil {
				targetTime = parsedISO.In(time.Local)
			} else {
				log.Warnf("Invalid time format %q for /set/synctime (expected 'now' or 'YYYY-MM-DD HH:MM:SS')", payloadStr)
				return
			}
		}
		if err := cl.SetRegister(protocol.SetSyncTimeRegister, protocol.EncodeSyncTime(targetTime)); err != nil {
			log.Infof("Error syncing vehicle clock: %v", err)
		}

	case relTopic == "/set/parkinglights":
		if on, ok := parseOnOffBool(payloadStr); ok {
			v := byte(0x02)
			if on {
				v = 0x01
			}
			if err := cl.SetRegister(protocol.SetParkingLightsRegister, []byte{v}); err != nil {
				log.Infof("Error setting parking lights: %v", err)
			}
		}

	case relTopic == "/set/headlights":
		if on, ok := parseOnOffBool(payloadStr); ok {
			v := byte(0x02)
			if on {
				v = 0x01
			}
			if err := cl.SetRegister(protocol.SetHeadlightsRegister, []byte{v}); err != nil {
				log.Infof("Error setting headlights: %v", err)
			}
		}

	case relTopic == "/set/lights/locate":
		if !atomic.CompareAndSwapInt32(&m.locateRunning, 0, 1) {
			return
		}
		go func(phevConn *client.Client) {
			defer atomic.StoreInt32(&m.locateRunning, 0)
			if err := phevConn.SetRegister(protocol.SetParkingLightsRegister, []byte{0x01}); err != nil {
				log.Infof("Error flashing parking lights on: %v", err)
				return
			}
			time.Sleep(2 * time.Second)
			if err := phevConn.SetRegister(protocol.SetParkingLightsRegister, []byte{0x02}); err != nil {
				log.Infof("Error flashing parking lights off: %v", err)
			}
		}(cl)

	case relTopic == "/set/alarm/clear":
		if err := cl.SetRegister(protocol.SetAckTheftAlarmRegister, []byte{0x01}); err != nil {
			log.Infof("Error clearing theft alarm notification: %v", err)
		}

	case relTopic == "/set/wifi/channel":
		ch, err := strconv.Atoi(payloadStr)
		if err != nil || ch < 1 || ch > 13 {
			log.Warnf("Invalid WiFi channel %q (expected 1..13)", payloadStr)
			return
		}
		if err := cl.SetRegister(protocol.SetWiFiChannelRegister, []byte{byte(ch)}); err != nil {
			log.Infof("Error setting WiFi channel: %v", err)
		}

	case relTopic == "/set/wifi/always_on":
		if on, ok := parseOnOffBool(payloadStr); ok {
			v := byte(0x00)
			if on {
				v = 0x01
			}
			if err := cl.SetRegister(protocol.SetWiFiAlwaysOnRegister, []byte{v}); err != nil {
				log.Infof("Error setting WiFi always-on mode: %v", err)
			}
		}

	case relTopic == "/set/cancelchargetimer":
		if on, ok := parseOnOffBool(payloadStr); ok && !on {
			// Turning OFF "Disable Charge Timer" re-enables the charge timer schedule.
			if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{0x02}); err != nil {
				log.Infof("Error enabling charge timer: %v", err)
			}
			return
		}
		m.overrideChargeTimerNow(cl)

	case relTopic == "/set/charge/now":
		m.overrideChargeTimerNow(cl)

	case relTopic == "/set/charge/timer/master":
		if on, ok := parseOnOffBool(payloadStr); ok {
			v := byte(0x01)
			if on {
				v = 0x02
			}
			if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{v}); err != nil {
				log.Infof("Error toggling master charge timer: %v", err)
			}
		}

	case strings.HasPrefix(relTopic, "/set/charge/timer/"):
		m.handleChargeTimerCommand(cl, strings.TrimPrefix(relTopic, "/set/charge/timer/"), payloadStr)

	case relTopic == "/set/climate/timer/master":
		if on, ok := parseOnOffBool(payloadStr); ok {
			v := byte(0x01)
			if on {
				v = 0x02
			}
			if err := cl.SetRegister(protocol.SetClimateTimerMasterReg, []byte{v}); err != nil {
				log.Infof("Error toggling master climate timer: %v", err)
			}
		}

	case relTopic == "/set/climate/timer/mode":
		mode := strings.ToLower(payloadStr)
		if mode != "heat" && mode != "cool" && mode != "windscreen" {
			log.Warnf("Invalid climate timer mode %q (expected heat, cool, or windscreen)", payloadStr)
			return
		}
		m.mu.Lock()
		m.climateSchedule.Mode = mode
		write1a := m.climateSchedule.EncodeWrite0x1a()
		anyEnabled := m.climateSchedule.AnyEnabled()
		m.mu.Unlock()

		if err := cl.SetRegister(protocol.SetClimateTimersMY18Reg, write1a); err != nil {
			log.Infof("Error writing climate timer mode: %v", err)
			return
		}
		masterByte := byte(0x01)
		if anyEnabled {
			masterByte = 0x02
		}
		_ = cl.SetRegister(protocol.SetClimateTimerMasterReg, []byte{masterByte})
		m.publish("/climate/timer/mode", mode)

	case strings.HasPrefix(relTopic, "/set/climate/timer/"):
		m.handleClimateTimerCommand(cl, strings.TrimPrefix(relTopic, "/set/climate/timer/"), payloadStr)

	case relTopic == "/set/climate/reset_error":
		if err := cl.SetRegister(protocol.SetAckPreACTermRegister, []byte{0x01}); err != nil {
			log.Infof("Error acknowledging Pre-AC termination: %v", err)
		}

	case relTopic == "/set/climate/state":
		lower := strings.ToLower(payloadStr)
		if lower == "reset" {
			if err := cl.SetRegister(protocol.SetAckPreACTermRegister, []byte{0x01}); err != nil {
				log.Infof("Error acknowledging Pre-AC termination: %v", err)
			}
		} else if lower == "off" {
			m.sendImmediateClimate(cl, 0x00, 0, 0)
		}

	case relTopic == "/set/climate/duration":
		dur, err := strconv.Atoi(payloadStr)
		if err != nil || (dur != 10 && dur != 20 && dur != 30) {
			log.Warnf("Invalid climate duration %q (expected 10, 20, or 30)", payloadStr)
			return
		}
		m.mu.Lock()
		m.climate.selectedDuration = dur
		m.mu.Unlock()
		m.publish("/climate/duration", strconv.Itoa(dur))

	case relTopic == "/set/climate/delay":
		delay, err := strconv.Atoi(payloadStr)
		if err != nil || (delay != 0 && delay != 5 && delay != 10) {
			log.Warnf("Invalid climate start delay %q (expected 0, 5, or 10)", payloadStr)
			return
		}
		m.mu.Lock()
		m.climate.selectedDelay = delay
		m.mu.Unlock()
		m.publish("/climate/delay", strconv.Itoa(delay))

	case strings.HasPrefix(relTopic, "/set/climate/"):
		m.handleImmediateClimateTopic(cl, strings.TrimPrefix(relTopic, "/set/climate/"), payloadStr)

	case strings.HasPrefix(relTopic, "/set/settings/"):
		slug := strings.TrimPrefix(relTopic, "/set/settings/")
		m.handleVehicleSettingCommand(cl, slug, payloadStr)

	default:
		log.Errorf("Unknown command topic from MQTT: %s", fullTopic)
	}
}

func (m *mqttClient) overrideChargeTimerNow(cl *client.Client) {
	if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{0x01}); err != nil {
		log.Infof("Error setting register 0x17 (0x01): %v", err)
		return
	}
	if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{0x11}); err != nil {
		log.Infof("Error setting register 0x17 (0x11): %v", err)
	}
}

func (m *mqttClient) handleImmediateClimateTopic(cl *client.Client, sub, payloadStr string) {
	lower := strings.ToLower(payloadStr)
	modeMap := map[string]byte{"off": 0x00, "cool": 0x01, "heat": 0x02, "windscreen": 0x03}

	m.mu.RLock()
	selDur := m.climate.selectedDuration
	selDelay := m.climate.selectedDelay
	m.mu.RUnlock()

	var mode byte
	durMin := selDur

	if sub == "mode" {
		var ok bool
		mode, ok = modeMap[lower]
		if !ok {
			log.Errorf("Unknown climate mode payload: %s", payloadStr)
			return
		}
	} else {
		var ok bool
		mode, ok = modeMap[sub]
		if !ok {
			log.Errorf("Unknown climate subtopic: %s", sub)
			return
		}
		switch lower {
		case "off", "false", "0":
			mode = 0x00
		case "on", "true", "1":
			// keep selected duration
		case "10", "20", "30":
			durMin, _ = strconv.Atoi(lower)
		default:
			log.Errorf("Unknown climate payload %q for %s", payloadStr, sub)
			return
		}
	}

	var durIdx byte
	switch durMin {
	case 20:
		durIdx = 0x01
	case 30:
		durIdx = 0x02
	default:
		durIdx = 0x00
	}

	var delayIdx byte
	switch selDelay {
	case 5:
		delayIdx = 0x01
	case 10:
		delayIdx = 0x02
	default:
		delayIdx = 0x00
	}

	m.sendImmediateClimate(cl, mode, durIdx, delayIdx)
}

func (m *mqttClient) sendImmediateClimate(cl *client.Client, mode, durIdx, delayIdx byte) {
	if cl.ModelYear == client.ModelYear14 {
		m.mu.RLock()
		reg22 := append([]byte(nil), m.my14Reg22...)
		reg19 := append([]byte(nil), m.my14Reg19...)
		m.mu.RUnlock()

		registerPayload := bytes.Repeat([]byte{0xff}, 15)
		if len(reg22) == 6 {
			copy(registerPayload[0:6], reg22)
		} else {
			registerPayload[0] = 0x00
			registerPayload[1] = 0x00
		}
		registerPayload[6] = (mode & 0x0f) | ((durIdx & 0x03) << 4)
		if len(reg19) >= 9 {
			copy(registerPayload[7:15], reg19[1:9])
		}
		if err := cl.SetRegister(protocol.SetACModeRegisterMY14, registerPayload); err != nil {
			log.Infof("Error setting MY14 AC mode: %v", err)
			return
		}
		acEnabled := byte(0x02)
		if mode == 0x00 {
			acEnabled = 0x01
		}
		if err := cl.SetRegister(protocol.SetACEnabledRegisterMY14, []byte{acEnabled}); err != nil {
			log.Infof("Error setting MY14 AC enabled state: %v", err)
		}
		return
	}

	// MY18 / MY20 / MY24
	if mode == 0x00 {
		if err := cl.SetRegister(protocol.SetACModeRegisterMY18, []byte{0x01, 0x00, 0x00, 0x00}); err != nil {
			log.Infof("Error stopping MY18 AC: %v", err)
		}
		return
	}
	if err := cl.SetRegister(protocol.SetACModeRegisterMY18, []byte{0x02, mode, durIdx, delayIdx}); err != nil {
		log.Infof("Error starting MY18 AC: %v", err)
	}
}

func (m *mqttClient) handleClimateTimerCommand(cl *client.Client, rest, payload string) {
	parts := strings.Split(rest, "/")
	slotNum, err := strconv.Atoi(parts[0])
	if err != nil || slotNum < 1 || slotNum > 5 {
		log.Warnf("Invalid climate timer slot in %q (expected 1..5)", rest)
		return
	}
	field := ""
	if len(parts) > 1 {
		field = parts[1]
	}

	m.mu.Lock()
	slot := m.climateSchedule.Timers[slotNum-1]
	slot.Index = slotNum

	switch field {
	case "":
		if strings.EqualFold(payload, "clear") || strings.EqualFold(payload, "reset") {
			slot = protocol.DefaultClimateTimerSlot(slotNum)
		} else if err := slot.ApplyJSON(payload); err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid JSON for climate timer %d: %v", slotNum, err)
			return
		}
	case "clear":
		slot = protocol.DefaultClimateTimerSlot(slotNum)
	case "enabled":
		on, ok := parseOnOffBool(payload)
		if !ok {
			m.mu.Unlock()
			log.Warnf("Invalid enabled value %q for climate timer %d", payload, slotNum)
			return
		}
		slot.Enabled = on
		if on {
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	case "time":
		h, mn, cfg, err := protocol.ParseTimerHHMM(payload)
		if err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid time %q for climate timer %d: %v", payload, slotNum, err)
			return
		}
		if !cfg {
			slot = protocol.DefaultClimateTimerSlot(slotNum)
		} else {
			slot.Hour = h
			slot.Minute = mn
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	case "duration":
		dur, err := strconv.Atoi(payload)
		if err != nil || (dur != 10 && dur != 20 && dur != 30) {
			m.mu.Unlock()
			log.Warnf("Invalid duration %q for climate timer %d (expected 10, 20, or 30)", payload, slotNum)
			return
		}
		slot.Duration = dur
		slot.Configured = true
		slot.EnsureValidDefaults()
	case "days":
		mask, err := protocol.ParseDaysMask(payload)
		if err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid days %q for climate timer %d: %v", payload, slotNum, err)
			return
		}
		if mask == protocol.DaysNone {
			slot.Enabled = false
			slot.DaysMask = protocol.DaysNone
		} else {
			slot.DaysMask = mask
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	default:
		m.mu.Unlock()
		log.Warnf("Unknown climate timer field %q", field)
		return
	}

	m.climateSchedule.Timers[slotNum-1] = slot
	anyEnabled := m.climateSchedule.AnyEnabled()
	m.climateSchedule.MasterEnabled = anyEnabled
	write1a := m.climateSchedule.EncodeWrite0x1a()
	schedSnap := m.climateSchedule
	m.mu.Unlock()

	if err := cl.SetRegister(protocol.SetClimateTimersMY18Reg, write1a); err != nil {
		log.Infof("Error writing climate timer schedule 0x1a: %v", err)
		return
	}
	masterByte := byte(0x01)
	if anyEnabled {
		masterByte = 0x02
	}
	if err := cl.SetRegister(protocol.SetClimateTimerMasterReg, []byte{masterByte}); err != nil {
		log.Infof("Error writing climate timer master 0x03: %v", err)
		return
	}
	m.publishClimateSchedule(&schedSnap)
}

func (m *mqttClient) handleChargeTimerCommand(cl *client.Client, rest, payload string) {
	parts := strings.Split(rest, "/")
	slotNum, err := strconv.Atoi(parts[0])
	if err != nil || slotNum < 1 || slotNum > 5 {
		log.Warnf("Invalid charge timer slot in %q (expected 1..5)", rest)
		return
	}
	field := ""
	if len(parts) > 1 {
		field = parts[1]
	}

	m.mu.Lock()
	slot := m.chargeSchedule.Timers[slotNum-1]
	slot.Index = slotNum

	switch field {
	case "":
		if strings.EqualFold(payload, "clear") || strings.EqualFold(payload, "reset") {
			slot = protocol.DefaultChargeTimerSlot(slotNum)
		} else if err := slot.ApplyJSON(payload); err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid JSON for charge timer %d: %v", slotNum, err)
			return
		}
	case "clear":
		slot = protocol.DefaultChargeTimerSlot(slotNum)
	case "enabled":
		on, ok := parseOnOffBool(payload)
		if !ok {
			m.mu.Unlock()
			log.Warnf("Invalid enabled value %q for charge timer %d", payload, slotNum)
			return
		}
		slot.Enabled = on
		if on {
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	case "start":
		h, mn, cfg, err := protocol.ParseTimerHHMM(payload)
		if err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid start time %q for charge timer %d: %v", payload, slotNum, err)
			return
		}
		if !cfg {
			slot = protocol.DefaultChargeTimerSlot(slotNum)
		} else {
			slot.StartHour = h
			slot.StartMinute = mn
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	case "end":
		h, mn, cfg, err := protocol.ParseTimerHHMM(payload)
		if err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid end time %q for charge timer %d: %v", payload, slotNum, err)
			return
		}
		if !cfg {
			slot = protocol.DefaultChargeTimerSlot(slotNum)
		} else {
			slot.EndHour = h
			slot.EndMinute = mn
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	case "full_charge":
		on, ok := parseOnOffBool(payload)
		if !ok {
			m.mu.Unlock()
			log.Warnf("Invalid full_charge value %q for charge timer %d", payload, slotNum)
			return
		}
		slot.FullCharge = on
		slot.Configured = true
		slot.EnsureValidDefaults()
	case "days":
		mask, err := protocol.ParseDaysMask(payload)
		if err != nil {
			m.mu.Unlock()
			log.Warnf("Invalid days %q for charge timer %d: %v", payload, slotNum, err)
			return
		}
		if mask == protocol.DaysNone {
			slot.Enabled = false
			slot.DaysMask = protocol.DaysNone
		} else {
			slot.DaysMask = mask
			slot.Configured = true
			slot.EnsureValidDefaults()
		}
	default:
		m.mu.Unlock()
		log.Warnf("Unknown charge timer field %q", field)
		return
	}

	m.chargeSchedule.Timers[slotNum-1] = slot
	anyEnabled := m.chargeSchedule.AnyEnabled()
	m.chargeSchedule.MasterEnabled = anyEnabled
	write19 := m.chargeSchedule.EncodeWrite0x19()
	schedSnap := m.chargeSchedule
	m.mu.Unlock()

	if err := cl.SetRegister(protocol.SetChargeTimersMY18Reg, write19); err != nil {
		log.Infof("Error writing charge timer schedule 0x19: %v", err)
		return
	}
	masterByte := byte(0x01)
	if anyEnabled {
		masterByte = 0x02
	}
	if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{masterByte}); err != nil {
		log.Infof("Error writing charge timer master 0x17: %v", err)
		return
	}
	m.publishChargeSchedule(&schedSnap)
}

func (m *mqttClient) handleVehicleSettingCommand(cl *client.Client, slugOrID, payload string) {
	if strings.EqualFold(slugOrID, "factory_reset") && (strings.EqualFold(payload, "press") || strings.EqualFold(payload, "reset")) {
		if err := cl.SetRegister(protocol.SetUpdateSettingRegister, protocol.EncodeFactoryResetSettings()); err != nil {
			log.Infof("Error sending settings factory reset: %v", err)
			return
		}
		if err := cl.SetRegister(protocol.SetCommitSettingsRegister, protocol.EncodeCommitSettings()); err != nil {
			log.Infof("Error committing settings factory reset: %v", err)
		}
		return
	}

	vs, ok := m.settings.GetBySlug(slugOrID)
	if !ok || !vs.Supported() {
		log.Warnf("Vehicle setting %q is unknown or unsupported on this vehicle", slugOrID)
		return
	}
	optIdx, err := vs.ResolveOption(payload)
	if err != nil {
		log.Warnf("Invalid option for setting %s: %v", vs.Slug, err)
		return
	}
	if err := cl.SetRegister(protocol.SetUpdateSettingRegister, protocol.EncodeUpdateSetting(vs.ID, optIdx)); err != nil {
		log.Infof("Error staging vehicle setting 0x%02x (%s): %v", vs.ID, vs.Slug, err)
		return
	}
	if err := cl.SetRegister(protocol.SetCommitSettingsRegister, protocol.EncodeCommitSettings()); err != nil {
		log.Infof("Error committing vehicle setting 0x%02x (%s): %v", vs.ID, vs.Slug, err)
		return
	}
	m.settings.UpdateValue(vs.ID, optIdx)
	if updated, ok2 := m.settings.Get(vs.ID); ok2 {
		m.publish("/settings/"+updated.Slug, updated.ValueName())
	}
}

func (m *mqttClient) handlePhev() error {
	address := viper.GetString("address")
	phev, err := client.New(client.AddressOption(address))
	if err != nil {
		return err
	}
	defer func() {
		_ = phev.Close()
		m.clearPhevIfMatch(phev)
	}()

	if err := phev.Connect(); err != nil {
		return err
	}
	m.setPhev(phev, false)

	if err := phev.Start(); err != nil {
		return err
	}
	m.setPhev(phev, true)
	m.mu.Lock()
	m.lastConnect = time.Now()
	m.lastError = nil
	m.mu.Unlock()

	m.client.Publish(m.topic("/available"), 0, true, "online")
	m.publish("/model_year", phev.ModelYear.String())

	defer func() {
		m.mu.Lock()
		m.lastConnect = time.Now()
		m.mu.Unlock()
	}()

	var encodingErrorCount int
	var lastEncodingError time.Time

	var updaterC <-chan time.Time
	var updaterTicker *time.Ticker
	if m.updateInterval > 0 {
		updaterTicker = time.NewTicker(m.updateInterval)
		updaterC = updaterTicker.C
		defer updaterTicker.Stop()
	}

	for {
		select {
		case <-updaterC:
			if atomic.CompareAndSwapInt32(&m.pollInFlight, 0, 1) {
				go func(cl *client.Client) {
					defer atomic.StoreInt32(&m.pollInFlight, 0)
					if err := cl.SetRegister(protocol.SetRequestUpdateRegister, []byte{0x03}); err != nil {
						log.Debugf("Periodic status poll failed: %v", err)
					}
				}(phev)
			}
		case msg, ok := <-phev.Recv:
			if !ok {
				log.Infof("Connection closed.")
				return fmt.Errorf("Connection closed.")
			}
			m.mu.Lock()
			m.lastConnect = time.Now()
			m.mu.Unlock()

			switch msg.Type {
			case protocol.CmdInBadEncoding:
				if time.Since(lastEncodingError) > 15*time.Second {
					encodingErrorCount = 0
				}
				if encodingErrorCount > 50 {
					return fmt.Errorf("Disconnecting due to too many errors")
				}
				encodingErrorCount++
				lastEncodingError = time.Now()
			case protocol.CmdInResp:
				if msg.Ack != protocol.Request {
					break
				}
				m.publishRegister(phev, msg)
				phev.SendMessage(&protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: msg.Register,
					Ack:      protocol.Ack,
					Xor:      msg.Xor,
					Data:     []byte{0x0},
				})
			}
		}
	}
}

var boolOnOff = map[bool]string{
	false: "off",
	true:  "on",
}

var boolOpen = map[bool]string{
	false: "closed",
	true:  "open",
}

func (m *mqttClient) publishClimateSchedule(sched *protocol.ClimateSchedule) {
	m.publish("/climate/timer/master", boolOnOff[sched.MasterEnabled])
	if sched.Mode != "" {
		m.publish("/climate/timer/mode", sched.Mode)
	}
	for i := 0; i < 5; i++ {
		slot := sched.Timers[i]
		n := i + 1
		base := fmt.Sprintf("/climate/timer/%d", n)
		m.publish(base, slot.ToJSON())
		m.publish(base+"/enabled", boolOnOff[slot.Configured && slot.Enabled])
		m.publish(base+"/time", slot.TimeString())
		dur := slot.Duration
		if dur != 10 && dur != 20 && dur != 30 {
			dur = 10
		}
		m.publish(base+"/duration", strconv.Itoa(dur))
		m.publish(base+"/days", slot.DaysString())
		m.publish(base+"/summary", slot.Summary())
	}
}

func (m *mqttClient) publishChargeSchedule(sched *protocol.ChargeSchedule) {
	m.publish("/charge/timer/master", boolOnOff[sched.MasterEnabled])
	m.publish("/charge/timer/cancelled", boolOnOff[!sched.MasterEnabled])
	for i := 0; i < 5; i++ {
		slot := sched.Timers[i]
		n := i + 1
		base := fmt.Sprintf("/charge/timer/%d", n)
		m.publish(base, slot.ToJSON())
		m.publish(base+"/enabled", boolOnOff[slot.Configured && slot.Enabled])
		m.publish(base+"/start", slot.StartString())
		m.publish(base+"/end", slot.EndString())
		m.publish(base+"/full_charge", boolOnOff[slot.FullCharge])
		m.publish(base+"/days", slot.DaysString())
		m.publish(base+"/summary", slot.Summary())
	}
}

func (m *mqttClient) publishRegister(cl *client.Client, msg *protocol.PhevMessage) {
	dataStr := hex.EncodeToString(msg.Data)
	m.publish(fmt.Sprintf("/register/%02x", msg.Register), dataStr)
	m.publish("/last_seen", time.Now().UTC().Format(time.RFC3339))

	// Cache MY14 schedule bytes if received so MY14 immediate AC writes preserve existing schedules.
	if msg.Register == 0x22 && len(msg.Data) == 6 {
		m.mu.Lock()
		m.my14Reg22 = append([]byte(nil), msg.Data...)
		m.mu.Unlock()
	} else if msg.Register == 0x19 && len(msg.Data) >= 9 {
		m.mu.Lock()
		m.my14Reg19 = append([]byte(nil), msg.Data...)
		m.mu.Unlock()
	}

	switch reg := msg.Reg.(type) {
	case *protocol.RegisterVIN:
		m.mu.Lock()
		m.vin = reg.VIN
		m.fullVIN = reg.FullVIN
		m.mu.Unlock()

		m.publish("/vin", reg.VIN)
		if reg.FullVIN != "" {
			m.publish("/vin_full", reg.FullVIN)
		}
		m.publish("/registrations", strconv.Itoa(reg.Registrations))
		m.publishHomeAssistantDiscovery(reg.VIN, m.prefix, "Phev")

	case *protocol.RegisterECUVersion:
		m.mu.Lock()
		prevVer := m.ecuVersion
		m.ecuVersion = reg.Version
		vin := m.vin
		m.mu.Unlock()

		m.publish("/ecuversion", reg.Version)
		if prevVer == "" && reg.Version != "" && vin != "" {
			// Update HA device sw_version once ECU version arrives.
			m.mu.Lock()
			m.haPublishedDiscovery = false
			m.mu.Unlock()
			m.publishHomeAssistantDiscovery(vin, m.prefix, "Phev")
		}

	case *protocol.RegisterCapability:
		m.mu.Lock()
		m.v2hSupported = reg.ExternalPowerSupplySupported
		vin := m.vin
		m.mu.Unlock()
		if reg.ExternalPowerSupplySupported && vin != "" {
			m.publishV2HDiscovery(vin, m.prefix, "Phev")
		}

	case *protocol.RegisterExternalPower:
		m.publish("/v2h/active", boolOnOff[reg.Active])
		if reg.Active {
			m.mu.RLock()
			vin := m.vin
			m.mu.RUnlock()
			if vin != "" {
				m.publishV2HDiscovery(vin, m.prefix, "Phev")
			}
		}

	case *protocol.RegisterVehicleVariant:
		m.mu.Lock()
		m.steeringSide = reg.SteeringSide
		m.mu.Unlock()
		m.publish("/variant/steering", reg.SteeringSide)

	case *protocol.RegisterWIFISSID:
		m.publish("/wifi/ssid", reg.SSID)

	case *protocol.RegisterWiFiChannel:
		if reg.Channel >= 1 && reg.Channel <= 13 {
			m.publish("/wifi/channel", strconv.Itoa(reg.Channel))
		}

	case *protocol.RegisterWiFiAnyConn:
		m.publish("/wifi/always_on", boolOnOff[reg.AlwaysOn])

	case *protocol.RegisterTime:
		m.publish("/clock", reg.String())
		if m.autoSyncTime {
			drift := time.Since(reg.Time)
			if drift < 0 {
				drift = -drift
			}
			m.mu.RLock()
			sinceSync := time.Since(m.lastTimeSync)
			m.mu.RUnlock()
			if drift > time.Minute && sinceSync > time.Hour {
				m.mu.Lock()
				m.lastTimeSync = time.Now()
				m.mu.Unlock()
				go func(phevConn *client.Client) {
					now := time.Now()
					log.Infof("Auto-syncing car clock (drift %v) to %s", drift.Round(time.Second), now.Format("2006-01-02 15:04:05"))
					_ = phevConn.SetRegister(protocol.SetSyncTimeRegister, protocol.EncodeSyncTime(now))
				}(cl)
			}
		}

	case *protocol.RegisterACMode:
		m.mu.Lock()
		m.climate.setMode(reg.Mode, reg.Duration)
		if reg.Mode == "cool" || reg.Mode == "heat" || reg.Mode == "windscreen" {
			m.climateSchedule.Mode = reg.Mode
		}
		states := m.climate.mqttStates()
		schedMode := m.climateSchedule.Mode
		m.mu.Unlock()

		for t, p := range states {
			m.publish(t, p)
		}
		if schedMode != "" {
			m.publish("/climate/timer/mode", schedMode)
		}

	case *protocol.RegisterPreACState:
		m.mu.Lock()
		m.climate.setState(reg)
		states := m.climate.mqttStates()
		m.mu.Unlock()
		for t, p := range states {
			m.publish(t, p)
		}

	case *protocol.RegisterACOperStatus:
		m.publish("/ignition/state", reg.Ignition)
		m.publish("/ignition/on", boolOnOff[reg.IgnitionOn])
		m.mu.Lock()
		m.climate.setOper(reg)
		states := m.climate.mqttStates()
		m.mu.Unlock()
		for t, p := range states {
			m.publish(t, p)
		}

	case *protocol.RegisterClimateTimers:
		m.mu.Lock()
		prevMode := m.climateSchedule.Mode
		m.climateSchedule = reg.Schedule
		if prevMode != "" {
			m.climateSchedule.Mode = prevMode
		}
		snap := m.climateSchedule
		m.mu.Unlock()
		m.publishClimateSchedule(&snap)

	case *protocol.RegisterChargeTimers:
		m.mu.Lock()
		prevMaster := m.chargeSchedule.MasterEnabled
		m.chargeSchedule = reg.Schedule
		m.chargeSchedule.MasterEnabled = prevMaster
		snap := m.chargeSchedule
		m.mu.Unlock()
		m.publishChargeSchedule(&snap)

	case *protocol.RegisterChargeTimerState:
		if reg.Reg == protocol.ChargeTimerStateRegister {
			m.mu.Lock()
			m.chargeSchedule.MasterEnabled = reg.Enabled
			m.mu.Unlock()
			m.publish("/charge/timer/master", boolOnOff[reg.Enabled])
			m.publish("/charge/timer/cancelled", boolOnOff[!reg.Enabled])
		}

	case *protocol.RegisterChargerSubStatus:
		if reg.Reg == protocol.QuickChargerStatusRegister {
			m.publish("/charge/quick_charger", boolOnOff[reg.QuickCharger])
		} else if reg.Reg == protocol.ChargerInhibitRegister {
			m.publish("/charge/timer_inhibited", boolOnOff[reg.TimerInhibited])
		}

	case *protocol.RegisterChargeStatus:
		m.publish("/charge/charging", boolOnOff[reg.Charging])
		m.publish("/charge/type", reg.ChargeType)
		if !reg.Charging {
			m.publish("/charge/remaining", "0")
		} else if reg.Remaining < 1000 {
			m.publish("/charge/remaining", strconv.Itoa(reg.Remaining))
		} else {
			log.Debugf("Ignoring charge remaining reading: %v", reg.Remaining)
			m.mu.RLock()
			cache := m.mqttData["/charge/remaining"]
			m.mu.RUnlock()
			if cache != "" {
				m.publish("/charge/remaining", cache)
			}
		}

	case *protocol.RegisterChargePlug:
		if reg.Connected {
			m.publish("/charge/plug", "connected")
		} else {
			m.publish("/charge/plug", "unplugged")
		}
		m.publish("/charge/plug_state", reg.PlugState)
		m.publish("/charge/timer_inhibited", boolOnOff[reg.WaitingForTimer])

	case *protocol.RegisterDoorStatus:
		m.mu.RLock()
		steering := m.steeringSide
		m.mu.RUnlock()

		m.publish("/door/locked", boolOpen[!reg.Locked])
		m.publish("/door/rear_left", boolOpen[reg.RearLeft])
		m.publish("/door/rear_right", boolOpen[reg.RearRight])
		m.publish("/door/driver", boolOpen[reg.Driver])
		m.publish("/door/front_passenger", boolOpen[reg.FrontPassenger])
		if steering == "LHD" {
			m.publish("/door/front_left", boolOpen[reg.Driver])
			m.publish("/door/front_right", boolOpen[reg.FrontPassenger])
		} else {
			m.publish("/door/front_right", boolOpen[reg.Driver])
			m.publish("/door/front_left", boolOpen[reg.FrontPassenger])
		}
		m.publish("/door/bonnet", boolOpen[reg.Bonnet])
		m.publish("/door/boot", boolOpen[reg.Boot])
		m.publish("/lights/head", boolOnOff[reg.Headlights])

	case *protocol.RegisterBatteryLevel:
		if reg.Valid {
			m.publish("/battery/level", strconv.Itoa(reg.Level))
		} else {
			m.mu.RLock()
			cache := m.mqttData["/battery/level"]
			m.mu.RUnlock()
			if cache != "" {
				m.publish("/battery/level", cache)
				log.Debugf("Ignoring invalid battery level reading %v, keeping %v", reg.Level, cache)
			}
		}
		m.publish("/lights/parking", boolOnOff[reg.ParkingLights])

	case *protocol.RegisterBatteryWarning:
		m.publish("/battery/warning", reg.WarningText)
		m.publish("/battery/warning_active", boolOnOff[reg.WarningCode > 0])
		m.publish("/battery/warming", boolOnOff[reg.WarmingActive])

	case *protocol.RegisterLightStatus:
		m.publish("/lights/interior", boolOnOff[reg.Interior])
		m.publish("/lights/hazard", boolOnOff[reg.Hazard])
		m.publish("/alarm/state", reg.TheftAlarm)
		m.publish("/alarm/triggered", boolOnOff[reg.TheftAlarmTriggered])

	case *protocol.RegisterTheftAlarmEvent:
		if reg.CauseCode == 0 {
			m.publish("/alarm/last_event", "none")
		} else if reg.ValidTime {
			m.publish("/alarm/last_event", fmt.Sprintf("%s (%s)", reg.CauseDescription, reg.Time.Format("2006-01-02 15:04:05")))
		} else {
			m.publish("/alarm/last_event", reg.CauseDescription)
		}

	case *protocol.RegisterSettings:
		_ = m.settings.FromRegister(msg.Data)
		m.mu.RLock()
		vin := m.vin
		m.mu.RUnlock()
		for _, vs := range reg.Items {
			if vs.Supported() {
				m.publish("/settings/"+vs.Slug, vs.ValueName())
				if vin != "" {
					m.publishSettingDiscovery(vin, m.prefix, "Phev", vs)
				}
			}
		}
	}
}

func (m *mqttClient) haDeviceMap(vin string) map[string]interface{} {
	m.mu.RLock()
	ecu := m.ecuVersion
	m.mu.RUnlock()

	dev := map[string]interface{}{
		"name":         "PHEV " + vin,
		"identifiers":  []string{"phev-" + vin},
		"manufacturer": "Mitsubishi",
		"model":        "Outlander PHEV",
	}
	if ecu != "" {
		dev["sw_version"] = ecu
	}
	return dev
}

func (m *mqttClient) sensorAvtyTopic() string {
	if m.haKeepSensorsOnline {
		return "~/bridge/available"
	}
	return "~/available"
}

func (m *mqttClient) publishDiscoveryJSON(component, objectID string, cfg map[string]interface{}) {
	if !m.haDiscovery || m.haDiscoveryPrefix == "" {
		return
	}
	discTopic := fmt.Sprintf("%s/%s/%s/config", m.haDiscoveryPrefix, component, objectID)
	payload, err := json.Marshal(cfg)
	if err != nil {
		log.Errorf("Failed to marshal HA discovery for %s: %v", discTopic, err)
		return
	}
	if token := m.client.Publish(discTopic, 0, true, payload); token.Wait() && token.Error() != nil {
		log.Errorf("Error publishing HA discovery %s: %v", discTopic, token.Error())
	}
}

func (m *mqttClient) publishSettingDiscovery(vin, baseTopic, name string, vs *protocol.VehicleSetting) {
	if !m.haDiscovery || !vs.Supported() {
		return
	}
	m.mu.Lock()
	if m.discoveredSettings[vs.Slug] {
		m.mu.Unlock()
		return
	}
	m.discoveredSettings[vs.Slug] = true
	m.mu.Unlock()

	dev := m.haDeviceMap(vin)
	objectID := fmt.Sprintf("%s_setting_%s", vin, vs.Slug)
	m.publishDiscoveryJSON("select", objectID, map[string]interface{}{
		"name":            fmt.Sprintf("%s %s", name, vs.Name),
		"icon":            "mdi:car-cog",
		"entity_category": "config",
		"state_topic":     "~/settings/" + vs.Slug,
		"command_topic":   "~/set/settings/" + vs.Slug,
		"options":         vs.SupportedOptions(),
		"avty_t":          "~/available",
		"unique_id":       objectID,
		"dev":             dev,
		"~":               baseTopic,
	})
}

func (m *mqttClient) publishV2HDiscovery(vin, baseTopic, name string) {
	if !m.haDiscovery {
		return
	}
	m.mu.Lock()
	if m.discoveredV2H {
		m.mu.Unlock()
		return
	}
	m.discoveredV2H = true
	m.mu.Unlock()

	dev := m.haDeviceMap(vin)
	objectID := fmt.Sprintf("%s_v2h_active", vin)
	m.publishDiscoveryJSON("binary_sensor", objectID, map[string]interface{}{
		"name":         fmt.Sprintf("%s External Power Supply (V2H)", name),
		"device_class": "power",
		"icon":         "mdi:home-lightning-bolt",
		"state_topic":  "~/v2h/active",
		"payload_on":   "on",
		"payload_off":  "off",
		"avty_t":       m.sensorAvtyTopic(),
		"unique_id":    objectID,
		"dev":          dev,
		"~":            baseTopic,
	})
}

type haEntitySpec struct {
	component string
	suffix    string
	uniqueID  string // optional override if historical unique_id differed from suffix
	fields    map[string]interface{}
}

// publishHomeAssistantDiscovery publishes Home Assistant MQTT Auto-Discovery configs for all vehicle entities.
func (m *mqttClient) publishHomeAssistantDiscovery(vin, topic, name string) {
	if !m.haDiscovery || vin == "" {
		return
	}
	m.mu.Lock()
	if m.haPublishedDiscovery {
		m.mu.Unlock()
		return
	}
	m.haPublishedDiscovery = true
	v2h := m.v2hSupported
	m.mu.Unlock()

	dev := m.haDeviceMap(vin)
	sensAvty := m.sensorAvtyTopic()

	entities := []haEntitySpec{
		// Bridge / Connectivity
		{
			component: "binary_sensor",
			suffix:    "vehicle_connected",
			fields: map[string]interface{}{
				"device_class": "connectivity",
				"name":         name + " Vehicle WiFi Connected",
				"state_topic":  "~/available",
				"payload_on":   "online",
				"payload_off":  "offline",
				"avty_t":       "~/bridge/available",
			},
		},
		{
			component: "sensor",
			suffix:    "last_seen",
			fields: map[string]interface{}{
				"device_class":    "timestamp",
				"entity_category": "diagnostic",
				"name":            name + " Last Seen",
				"icon":            "mdi:clock-check-outline",
				"state_topic":     "~/last_seen",
				"avty_t":          "~/bridge/available",
			},
		},

		// Doors & Locks (preserving exact historical unique_ids)
		{
			component: "binary_sensor",
			suffix:    "door_locked",
			fields: map[string]interface{}{
				"device_class": "lock",
				"name":         name + " Locked",
				"state_topic":  "~/door/locked",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_bonnet",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Bonnet",
				"state_topic":  "~/door/bonnet",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_boot",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Boot",
				"state_topic":  "~/door/boot",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_front_passenger",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Front Passenger Door",
				"state_topic":  "~/door/front_passenger",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_driver",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Driver Door",
				"state_topic":  "~/door/driver",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_rear_left",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Rear Left Door",
				"state_topic":  "~/door/rear_left",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "door_rear_right",
			fields: map[string]interface{}{
				"device_class": "door",
				"name":         name + " Rear Right Door",
				"state_topic":  "~/door/rear_right",
				"payload_off":  "closed",
				"payload_on":   "open",
				"avty_t":       sensAvty,
			},
		},

		// Battery & Charging
		{
			component: "sensor",
			suffix:    "battery_level",
			fields: map[string]interface{}{
				"device_class":        "battery",
				"name":                name + " Battery",
				"state_topic":         "~/battery/level",
				"state_class":         "measurement",
				"unit_of_measurement": "%",
				"avty_t":              sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "battery_warning",
			fields: map[string]interface{}{
				"device_class": "problem",
				"name":         name + " Battery Warning",
				"state_topic":  "~/battery/warning_active",
				"payload_on":   "on",
				"payload_off":  "off",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "battery_warning_status",
			fields: map[string]interface{}{
				"name":            name + " Battery Thermal Status",
				"icon":            "mdi:car-battery",
				"entity_category": "diagnostic",
				"state_topic":     "~/battery/warning",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "battery_warming",
			fields: map[string]interface{}{
				"device_class": "heat",
				"name":         name + " Battery Warming",
				"icon":         "mdi:thermometer-chevron-up",
				"state_topic":  "~/battery/warming",
				"payload_on":   "on",
				"payload_off":  "off",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "battery_charge_remaining",
			fields: map[string]interface{}{
				"name":                name + " Charge Remaining",
				"icon":                "mdi:timer-sand",
				"state_topic":         "~/charge/remaining",
				"unit_of_measurement": "min",
				"avty_t":              sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "charger_connected",
			fields: map[string]interface{}{
				"device_class": "plug",
				"name":         name + " Charger Connected",
				"state_topic":  "~/charge/plug",
				"payload_on":   "connected",
				"payload_off":  "unplugged",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "charge_plug_state",
			fields: map[string]interface{}{
				"name":        name + " Charge Plug Status",
				"icon":        "mdi:ev-plug-type1",
				"state_topic": "~/charge/plug_state",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "battery_charging",
			fields: map[string]interface{}{
				"device_class": "battery_charging",
				"name":         name + " Charging",
				"state_topic":  "~/charge/charging",
				"payload_on":   "on",
				"payload_off":  "off",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "charge_type",
			fields: map[string]interface{}{
				"name":        name + " Charge Type",
				"icon":        "mdi:ev-station",
				"state_topic": "~/charge/type",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "charge_timer_inhibited",
			fields: map[string]interface{}{
				"name":        name + " Waiting for Charge Timer",
				"icon":        "mdi:clock-alert-outline",
				"state_topic": "~/charge/timer_inhibited",
				"payload_on":  "on",
				"payload_off": "off",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "switch",
			suffix:    "cancel_charge_timer",
			fields: map[string]interface{}{
				"name":          name + " Disable Charge Timer",
				"icon":          "mdi:timer-off",
				"state_topic":   "~/charge/timer/cancelled",
				"command_topic": "~/set/cancelchargetimer",
				"payload_on":    "on",
				"payload_off":   "off",
				"avty_t":        "~/available",
			},
		},
		{
			component: "switch",
			suffix:    "charge_timer_master",
			fields: map[string]interface{}{
				"name":          name + " Charge Timer Master",
				"icon":          "mdi:battery-clock",
				"state_topic":   "~/charge/timer/master",
				"command_topic": "~/set/charge/timer/master",
				"payload_on":    "on",
				"payload_off":   "off",
				"avty_t":        "~/available",
			},
		},
		{
			component: "button",
			suffix:    "charge_now",
			fields: map[string]interface{}{
				"name":          name + " Charge Now (Override Timer)",
				"icon":          "mdi:flash",
				"command_topic": "~/set/charge/now",
				"payload_press": "press",
				"avty_t":        "~/available",
			},
		},

		// Immediate Climate Control
		{
			component: "switch",
			suffix:    "climate_heat",
			fields: map[string]interface{}{
				"name":          name + " Heat",
				"icon":          "mdi:weather-sunny",
				"state_topic":   "~/climate/heat",
				"command_topic": "~/set/climate/heat",
				"payload_off":   "off",
				"payload_on":    "on",
				"avty_t":        "~/available",
			},
		},
		{
			component: "switch",
			suffix:    "climate_cool",
			fields: map[string]interface{}{
				"name":          name + " cool",
				"icon":          "mdi:air-conditioner",
				"state_topic":   "~/climate/cool",
				"command_topic": "~/set/climate/cool",
				"payload_off":   "off",
				"payload_on":    "on",
				"avty_t":        "~/available",
			},
		},
		{
			component: "switch",
			suffix:    "climate_windscreen",
			fields: map[string]interface{}{
				"name":          name + " windscreen",
				"icon":          "mdi:car-defrost-front",
				"state_topic":   "~/climate/windscreen",
				"command_topic": "~/set/climate/windscreen",
				"payload_off":   "off",
				"payload_on":    "on",
				"avty_t":        "~/available",
			},
		},
		{
			component: "select",
			suffix:    "climate_on",
			fields: map[string]interface{}{
				"name":          name + " climate state",
				"icon":          "mdi:car-seat-heater",
				"state_topic":   "~/climate/mode",
				"command_topic": "~/set/climate/mode",
				"options":       []string{"off", "heat", "cool", "windscreen"},
				"avty_t":        "~/available",
			},
		},
		{
			component: "select",
			suffix:    "climate_duration",
			fields: map[string]interface{}{
				"name":          name + " Climate Duration (min)",
				"icon":          "mdi:timer-outline",
				"state_topic":   "~/climate/duration",
				"command_topic": "~/set/climate/duration",
				"options":       []string{"10", "20", "30"},
				"avty_t":        "~/available",
			},
		},
		{
			component: "select",
			suffix:    "climate_delay",
			fields: map[string]interface{}{
				"name":          name + " Climate Start Delay (min)",
				"icon":          "mdi:timer-play-outline",
				"state_topic":   "~/climate/delay",
				"command_topic": "~/set/climate/delay",
				"options":       []string{"0", "5", "10"},
				"avty_t":        "~/available",
			},
		},
		{
			component: "sensor",
			suffix:    "climate_status",
			fields: map[string]interface{}{
				"name":        name + " Climate Status",
				"icon":        "mdi:hvac",
				"state_topic": "~/climate/state",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "climate_stop_time",
			fields: map[string]interface{}{
				"name":        name + " Climate Stop Time",
				"icon":        "mdi:clock-end",
				"state_topic": "~/climate/stop_time",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "button",
			suffix:    "climate_reset_error",
			fields: map[string]interface{}{
				"name":          name + " Reset Climate Error",
				"icon":          "mdi:alert-circle-check-outline",
				"command_topic": "~/set/climate/reset_error",
				"payload_press": "press",
				"avty_t":        "~/available",
			},
		},

		// Climate Schedule Master Controls
		{
			component: "switch",
			suffix:    "climate_timer_master",
			fields: map[string]interface{}{
				"name":          name + " Climate Timer Master",
				"icon":          "mdi:calendar-clock",
				"state_topic":   "~/climate/timer/master",
				"command_topic": "~/set/climate/timer/master",
				"payload_on":    "on",
				"payload_off":   "off",
				"avty_t":        "~/available",
			},
		},
		{
			component: "select",
			suffix:    "climate_timer_mode",
			fields: map[string]interface{}{
				"name":          name + " Climate Timer Mode",
				"icon":          "mdi:thermostat",
				"state_topic":   "~/climate/timer/mode",
				"command_topic": "~/set/climate/timer/mode",
				"options":       []string{"heat", "cool", "windscreen"},
				"avty_t":        "~/available",
			},
		},

		// Ignition & Theft Alarm
		{
			component: "binary_sensor",
			suffix:    "ignition_on",
			fields: map[string]interface{}{
				"device_class": "power",
				"name":         name + " Ignition",
				"icon":         "mdi:key-variant",
				"state_topic":  "~/ignition/on",
				"payload_on":   "on",
				"payload_off":  "off",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "ignition_state",
			fields: map[string]interface{}{
				"name":        name + " Ignition State",
				"icon":        "mdi:engine-outline",
				"state_topic": "~/ignition/state",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "theft_alarm_state",
			fields: map[string]interface{}{
				"name":        name + " Theft Alarm State",
				"icon":        "mdi:shield-car",
				"state_topic": "~/alarm/state",
				"avty_t":      sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "theft_alarm_triggered",
			fields: map[string]interface{}{
				"device_class": "safety",
				"name":         name + " Theft Alarm Triggered",
				"icon":         "mdi:alarm-light",
				"state_topic":  "~/alarm/triggered",
				"payload_on":   "on",
				"payload_off":  "off",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "theft_alarm_last_event",
			fields: map[string]interface{}{
				"name":            name + " Last Theft Alarm Event",
				"icon":            "mdi:shield-alert-outline",
				"entity_category": "diagnostic",
				"state_topic":     "~/alarm/last_event",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "button",
			suffix:    "theft_alarm_clear",
			fields: map[string]interface{}{
				"name":            name + " Clear Alarm Notification",
				"icon":            "mdi:bell-cancel-outline",
				"entity_category": "config",
				"command_topic":   "~/set/alarm/clear",
				"payload_press":   "press",
				"avty_t":          "~/available",
			},
		},

		// Lights
		{
			component: "light",
			suffix:    "parkinglights",
			fields: map[string]interface{}{
				"name":          name + " Park Lights",
				"icon":          "mdi:car-parking-lights",
				"state_topic":   "~/lights/parking",
				"command_topic": "~/set/parkinglights",
				"payload_off":   "off",
				"payload_on":    "on",
				"avty_t":        "~/available",
			},
		},
		{
			component: "light",
			suffix:    "headlights",
			fields: map[string]interface{}{
				"name":          name + " Head Lights",
				"icon":          "mdi:car-light-dimmed",
				"state_topic":   "~/lights/head",
				"command_topic": "~/set/headlights",
				"payload_off":   "off",
				"payload_on":    "on",
				"avty_t":        "~/available",
			},
		},
		{
			component: "binary_sensor",
			suffix:    "interiorlights",
			fields: map[string]interface{}{
				"device_class": "light",
				"name":         name + " Interior Lights",
				"icon":         "mdi:lightbulb",
				"state_topic":  "~/lights/interior",
				"payload_off":  "off",
				"payload_on":   "on",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "binary_sensor",
			suffix:    "hazardlights",
			fields: map[string]interface{}{
				"device_class": "light",
				"name":         name + " Hazard Lights",
				"icon":         "mdi:hazard-lights",
				"state_topic":  "~/lights/hazard",
				"payload_off":  "off",
				"payload_on":   "on",
				"avty_t":       sensAvty,
			},
		},
		{
			component: "button",
			suffix:    "locate_car",
			fields: map[string]interface{}{
				"name":          name + " Locate Car (Flash Lights)",
				"icon":          "mdi:car-light-high",
				"command_topic": "~/set/lights/locate",
				"payload_press": "press",
				"avty_t":        "~/available",
			},
		},

		// Actions, Clock, Wi-Fi & Diagnostics
		{
			component: "button",
			suffix:    "reconnect_wifi",
			uniqueID:  vin + "_restart_wifi",
			fields: map[string]interface{}{
				"name":          name + " Restart Wifi connection",
				"icon":          "mdi:wifi-refresh",
				"command_topic": "~/connection",
				"payload_press": "restart",
				"avty_t":        "~/bridge/available",
			},
		},
		{
			component: "button",
			suffix:    "refresh_status",
			fields: map[string]interface{}{
				"name":          name + " Refresh Vehicle Status",
				"icon":          "mdi:refresh",
				"command_topic": "~/set/refresh",
				"payload_press": "press",
				"avty_t":        "~/available",
			},
		},
		{
			component: "sensor",
			suffix:    "car_clock",
			fields: map[string]interface{}{
				"name":            name + " Car Clock",
				"icon":            "mdi:clock-outline",
				"entity_category": "diagnostic",
				"state_topic":     "~/clock",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "button",
			suffix:    "sync_clock",
			fields: map[string]interface{}{
				"name":            name + " Sync Car Clock",
				"icon":            "mdi:clock-fast",
				"entity_category": "config",
				"command_topic":   "~/set/synctime",
				"payload_press":   "now",
				"avty_t":          "~/available",
			},
		},
		{
			component: "sensor",
			suffix:    "vin_full",
			fields: map[string]interface{}{
				"name":            name + " VIN",
				"icon":            "mdi:card-account-details-outline",
				"entity_category": "diagnostic",
				"state_topic":     "~/vin_full",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "ecu_version",
			fields: map[string]interface{}{
				"name":            name + " ECU Version",
				"icon":            "mdi:chip",
				"entity_category": "diagnostic",
				"state_topic":     "~/ecuversion",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "registered_phones",
			fields: map[string]interface{}{
				"name":            name + " Registered Smartphones",
				"icon":            "mdi:cellphone-key",
				"entity_category": "diagnostic",
				"state_topic":     "~/registrations",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "steering_side",
			fields: map[string]interface{}{
				"name":            name + " Steering Side",
				"icon":            "mdi:steering",
				"entity_category": "diagnostic",
				"state_topic":     "~/variant/steering",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "sensor",
			suffix:    "wifi_ssid",
			fields: map[string]interface{}{
				"name":            name + " WiFi SSID",
				"icon":            "mdi:wifi",
				"entity_category": "diagnostic",
				"state_topic":     "~/wifi/ssid",
				"avty_t":          sensAvty,
			},
		},
		{
			component: "select",
			suffix:    "wifi_channel",
			fields: map[string]interface{}{
				"name":            name + " WiFi Channel",
				"icon":            "mdi:access-point-network",
				"entity_category": "config",
				"state_topic":     "~/wifi/channel",
				"command_topic":   "~/set/wifi/channel",
				"options":         []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
				"avty_t":          "~/available",
			},
		},
		{
			component: "switch",
			suffix:    "wifi_always_on",
			fields: map[string]interface{}{
				"name":            name + " Keep WiFi Always Active",
				"icon":            "mdi:wifi-star",
				"entity_category": "config",
				"state_topic":     "~/wifi/always_on",
				"command_topic":   "~/set/wifi/always_on",
				"payload_on":      "on",
				"payload_off":     "off",
				"avty_t":          "~/available",
			},
		},
	}

	// Climate Timer Slots 1..5
	for i := 1; i <= 5; i++ {
		nStr := strconv.Itoa(i)
		entities = append(entities,
			haEntitySpec{
				component: "switch",
				suffix:    fmt.Sprintf("climate_timer_%d_enabled", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Climate Timer %s Enabled", name, nStr),
					"icon":          "mdi:radiator",
					"state_topic":   fmt.Sprintf("~/climate/timer/%s/enabled", nStr),
					"command_topic": fmt.Sprintf("~/set/climate/timer/%s/enabled", nStr),
					"payload_on":    "on",
					"payload_off":   "off",
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "text",
				suffix:    fmt.Sprintf("climate_timer_%d_time", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Climate Timer %s Time", name, nStr),
					"icon":          "mdi:clock-edit-outline",
					"state_topic":   fmt.Sprintf("~/climate/timer/%s/time", nStr),
					"command_topic": fmt.Sprintf("~/set/climate/timer/%s/time", nStr),
					"pattern":       `^([01]?[0-9]|2[0-3]):[0-5][0-9]$|^--:--$`,
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "select",
				suffix:    fmt.Sprintf("climate_timer_%d_duration", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Climate Timer %s Duration", name, nStr),
					"icon":          "mdi:timer-cog-outline",
					"state_topic":   fmt.Sprintf("~/climate/timer/%s/duration", nStr),
					"command_topic": fmt.Sprintf("~/set/climate/timer/%s/duration", nStr),
					"options":       []string{"10", "20", "30"},
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "text",
				suffix:    fmt.Sprintf("climate_timer_%d_days", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Climate Timer %s Days", name, nStr),
					"icon":          "mdi:calendar-week",
					"state_topic":   fmt.Sprintf("~/climate/timer/%s/days", nStr),
					"command_topic": fmt.Sprintf("~/set/climate/timer/%s/days", nStr),
					"avty_t":        "~/available",
				},
			},
		)
	}

	// Charge Timer Slots 1..5
	for i := 1; i <= 5; i++ {
		nStr := strconv.Itoa(i)
		entities = append(entities,
			haEntitySpec{
				component: "switch",
				suffix:    fmt.Sprintf("charge_timer_%d_enabled", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Charge Timer %s Enabled", name, nStr),
					"icon":          "mdi:ev-plug-type1",
					"state_topic":   fmt.Sprintf("~/charge/timer/%s/enabled", nStr),
					"command_topic": fmt.Sprintf("~/set/charge/timer/%s/enabled", nStr),
					"payload_on":    "on",
					"payload_off":   "off",
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "text",
				suffix:    fmt.Sprintf("charge_timer_%d_start", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Charge Timer %s Start", name, nStr),
					"icon":          "mdi:clock-start",
					"state_topic":   fmt.Sprintf("~/charge/timer/%s/start", nStr),
					"command_topic": fmt.Sprintf("~/set/charge/timer/%s/start", nStr),
					"pattern":       `^([01]?[0-9]|2[0-3]):[0-5][0-9]$|^--:--$`,
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "text",
				suffix:    fmt.Sprintf("charge_timer_%d_end", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Charge Timer %s End", name, nStr),
					"icon":          "mdi:clock-end",
					"state_topic":   fmt.Sprintf("~/charge/timer/%s/end", nStr),
					"command_topic": fmt.Sprintf("~/set/charge/timer/%s/end", nStr),
					"pattern":       `^([01]?[0-9]|2[0-3]):[0-5][0-9]$|^--:--$`,
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "switch",
				suffix:    fmt.Sprintf("charge_timer_%d_full_charge", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Charge Timer %s Full Charge", name, nStr),
					"icon":          "mdi:battery-charging-100",
					"state_topic":   fmt.Sprintf("~/charge/timer/%s/full_charge", nStr),
					"command_topic": fmt.Sprintf("~/set/charge/timer/%s/full_charge", nStr),
					"payload_on":    "on",
					"payload_off":   "off",
					"avty_t":        "~/available",
				},
			},
			haEntitySpec{
				component: "text",
				suffix:    fmt.Sprintf("charge_timer_%d_days", i),
				fields: map[string]interface{}{
					"name":          fmt.Sprintf("%s Charge Timer %s Days", name, nStr),
					"icon":          "mdi:calendar-week",
					"state_topic":   fmt.Sprintf("~/charge/timer/%s/days", nStr),
					"command_topic": fmt.Sprintf("~/set/charge/timer/%s/days", nStr),
					"avty_t":        "~/available",
				},
			},
		)
	}

	for _, ent := range entities {
		objectID := fmt.Sprintf("%s_%s", vin, ent.suffix)
		uid := objectID
		if ent.uniqueID != "" {
			uid = ent.uniqueID
		}
		cfg := make(map[string]interface{}, len(ent.fields)+3)
		for k, v := range ent.fields {
			cfg[k] = v
		}
		cfg["unique_id"] = uid
		cfg["dev"] = dev
		cfg["~"] = topic
		m.publishDiscoveryJSON(ent.component, objectID, cfg)
	}

	if v2h {
		m.publishV2HDiscovery(vin, topic, name)
	}

	for _, vs := range m.settings.SupportedSettings() {
		m.publishSettingDiscovery(vin, topic, name, vs)
	}
}

func init() {
	clientCmd.AddCommand(mqttCmd)

	mqttCmd.Flags().String("mqtt_server", "tcp://127.0.0.1:1883", "Address of MQTT server")
	mqttCmd.Flags().String("mqtt_username", "", "Username to login to MQTT server")
	mqttCmd.Flags().String("mqtt_password", "", "Password to login to MQTT server")
	mqttCmd.Flags().String("mqtt_topic_prefix", "phev", "Prefix for MQTT topics")
	mqttCmd.Flags().Bool("mqtt_disable_register_set_command", false, "Disable vehicle register setting via MQTT")
	mqttCmd.Flags().Bool("mqtt_retain", true, "Publish vehicle state topics with MQTT retain flag")
	mqttCmd.Flags().Bool("auto_sync_time", false, "Automatically sync vehicle RTC when drift exceeds 60s")
	mqttCmd.Flags().Bool("ha_discovery", true, "Enable Home Assistant MQTT discovery")
	mqttCmd.Flags().String("ha_discovery_prefix", "homeassistant", "Prefix for Home Assistant MQTT discovery")
	mqttCmd.Flags().Bool("ha_keep_sensors_available", false, "Keep read-only Home Assistant sensors available when vehicle WiFi sleeps")
	mqttCmd.Flags().Duration("update_interval", 5*time.Minute, "How often to request force updates")
	mqttCmd.Flags().Duration("wifi_restart_time", 0, "Attempt to restart Wifi if no connection for this long")
	mqttCmd.Flags().Duration("wifi_restart_retry_time", 2*time.Minute, "Interval to attempt Wifi restart")
	mqttCmd.Flags().String("wifi_restart_command", defaultWifiRestartCmd, "Command to restart Wifi connection to Phev")

	viper.BindPFlag("mqtt_server", mqttCmd.Flags().Lookup("mqtt_server"))
	viper.BindPFlag("mqtt_username", mqttCmd.Flags().Lookup("mqtt_username"))
	viper.BindPFlag("mqtt_password", mqttCmd.Flags().Lookup("mqtt_password"))
	viper.BindPFlag("mqtt_topic_prefix", mqttCmd.Flags().Lookup("mqtt_topic_prefix"))
	viper.BindPFlag("mqtt_disable_register_set_command", mqttCmd.Flags().Lookup("mqtt_disable_register_set_command"))
	viper.BindPFlag("mqtt_retain", mqttCmd.Flags().Lookup("mqtt_retain"))
	viper.BindPFlag("auto_sync_time", mqttCmd.Flags().Lookup("auto_sync_time"))
	viper.BindPFlag("ha_discovery", mqttCmd.Flags().Lookup("ha_discovery"))
	viper.BindPFlag("ha_discovery_prefix", mqttCmd.Flags().Lookup("ha_discovery_prefix"))
	viper.BindPFlag("ha_keep_sensors_available", mqttCmd.Flags().Lookup("ha_keep_sensors_available"))
	viper.BindPFlag("update_interval", mqttCmd.Flags().Lookup("update_interval"))
	viper.BindPFlag("wifi_restart_time", mqttCmd.Flags().Lookup("wifi_restart_time"))
	viper.BindPFlag("wifi_restart_retry_time", mqttCmd.Flags().Lookup("wifi_restart_retry_time"))
	viper.BindPFlag("wifi_restart_command", mqttCmd.Flags().Lookup("wifi_restart_command"))
}
