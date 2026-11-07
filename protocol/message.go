package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	CmdOutPingReq = 0xf3
	CmdInPingResp = 0x3f

	CmdOutSend = 0xf6
	CmdInResp  = 0x6f

	CmdInMy24StartReq   = 0x6e
	CmdOutMy24StartResp = 0xe6

	CmdInMy18StartReq   = 0x5e
	CmdOutMy18StartResp = 0xe5

	CmdInMy14StartReq   = 0x4e
	CmdOutMy14StartResp = 0xe4

	CmdInBadEncoding = 0xbb
	CmdInUnkn3       = 0xcc

	CmdInStartResp      = 0x2f
	CmdOutStartSendMy18 = 0xf2

	CmdInUnkn4 = 0x2e
)

const (
	Request byte = 0x0
	Ack     byte = 0x1
)

var ackStr = map[byte]string{
	0x0: "REQ",
	0x1: "ACK",
}

var messageStr = map[byte]string{
	0xf3: "PingReq",
	0x3f: "PingResp",
	0xf6: "SendCmd",
	0x6f: "RespCmd",
	0xe5: "StartResp18",
	0x5e: "StartReq18",
	0xf2: "StartSend",
	0x2f: "StartResp",
	0xe4: "StartResp14",
	0x4e: "StartReq14",
	0xe6: "StartResp24",
	0x6e: "StartReq24",
	0xbb: "BadEncoding",
	0xcc: "SessionClose",
	0x2e: "Error2E",
}

func isKnownPacketType(t byte) bool {
	_, ok := messageStr[t]
	return ok
}

type PhevMessage struct {
	Type          byte
	Length        byte
	Ack           byte
	Register      byte
	Data          []byte
	Checksum      byte
	Xor           byte
	ForceXor      bool
	Original      []byte
	OriginalXored []byte
	Reg           Register
}

func (p *PhevMessage) ShortForm() string {
	switch p.Type {
	case CmdInPingResp:
		return fmt.Sprintf("PING RESP     (id %x)", p.Register)

	case CmdOutPingReq:
		return fmt.Sprintf("PING REQ      (id %x)", p.Register)

	case CmdOutStartSendMy18:
		return fmt.Sprintf("START SEND18  (orig  %s)", hex.EncodeToString(p.Original))

	case CmdInStartResp:
		return fmt.Sprintf("START RESP    (orig: %s)", hex.EncodeToString(p.Original))

	case CmdOutSend:
		if p.Ack == Ack {
			return fmt.Sprintf("REGISTER ACK  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
		}
		return fmt.Sprintf("REGISTER SET  (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInResp:
		if p.Ack == Request {
			if p.Reg != nil {
				return fmt.Sprintf("REGISTER NTFY (reg 0x%02x data %s) [%s]", p.Register, hex.EncodeToString(p.Data), p.Reg.String())
			}
			return fmt.Sprintf("REGISTER NTFY (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))
		}
		return fmt.Sprintf("REGISTER SETACK (reg 0x%02x data %s)", p.Register, hex.EncodeToString(p.Data))

	case CmdInMy24StartReq:
		return fmt.Sprintf("START RECV24  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy24StartResp:
		return fmt.Sprintf("START SEND24  (orig %s)", hex.EncodeToString(p.Original))

	case CmdInMy18StartReq:
		return fmt.Sprintf("START RECV18  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy18StartResp:
		return fmt.Sprintf("START SEND18  (orig %s)", hex.EncodeToString(p.Original))

	case CmdInMy14StartReq:
		return fmt.Sprintf("START RECV14  (orig %s)", hex.EncodeToString(p.Original))

	case CmdOutMy14StartResp:
		return fmt.Sprintf("START SEND14  (orig %s)", hex.EncodeToString(p.Original))

	case CmdInBadEncoding:
		if len(p.Data) > 0 {
			return fmt.Sprintf("BAD ENCODING  (exp: 0x%02x)", p.Data[0])
		}
		return "BAD ENCODING"

	default:
		return p.String()
	}
}

func (p *PhevMessage) RawString() string {
	return hex.EncodeToString(p.Original)
}

func (p *PhevMessage) EncodeToBytes(key *SecurityKey) []byte {
	length := byte(len(p.Data) + 3)
	data := []byte{
		p.Type,
		length,
		p.Ack,
		p.Register,
	}
	data = append(data, p.Data...)
	data = append(data, Checksum(data))
	var xor byte
	if p.ForceXor {
		xor = p.Xor
	} else {
		switch p.Type {
		case CmdInMy24StartReq, CmdOutMy24StartResp, CmdInMy18StartReq, CmdOutMy18StartResp, CmdInMy14StartReq, CmdOutMy14StartResp:
			// No xor/key for these messages.
		case CmdOutSend:
			// Use then increment send key.
			xor = key.SKey(true)
		default:
			// Use but do not increment send key.
			xor = key.SKey(false)
		}
		p.Xor = xor
	}
	return XorMessageWith(data, xor)
}

func (p *PhevMessage) populateDecoded(decoded []byte, origXored []byte, xor byte, key *SecurityKey) error {
	if len(decoded) < 4 || len(decoded) < int(decoded[1])+2 {
		return fmt.Errorf("invalid packet length")
	}
	p.OriginalXored = origXored
	p.Type = decoded[0]
	p.Length = decoded[1] + 2
	p.Ack = decoded[2]
	p.Register = decoded[3]
	p.Data = decoded[4 : p.Length-1]
	p.Checksum = decoded[p.Length-1]
	p.Xor = xor
	p.Original = decoded

	if key != nil {
		switch p.Type {
		case CmdInMy24StartReq, CmdInMy18StartReq, CmdInMy14StartReq:
			key.Update(p.OriginalXored)
		case CmdInResp:
			key.RKey(true)
		case CmdOutSend:
			key.SKey(true)
		}
	}

	if p.Type == CmdInResp && p.Ack == Request {
		switch p.Register {
		case PreACNotificationRegister:
			p.Reg = new(RegisterPreACNotification)
		case BatteryWarningRegister:
			p.Reg = new(RegisterBatteryWarning)
		case CapabilityRegister:
			p.Reg = new(RegisterCapability)
		case ChargeTimersRegister:
			if len(p.Data) == 20 {
				p.Reg = new(RegisterChargeTimers)
			} else {
				p.Reg = new(RegisterGeneric)
			}
		case ClimateTimersRegister:
			if len(p.Data) == 16 {
				p.Reg = new(RegisterClimateTimers)
			} else {
				p.Reg = new(RegisterGeneric)
			}
		case VINAltRegister:
			if len(p.Data) == 20 {
				p.Reg = new(RegisterVIN)
			} else {
				p.Reg = new(RegisterGeneric)
			}
		case WiFiChannelRegister:
			p.Reg = new(RegisterWiFiChannel)
		case WiFiCommModeRegister:
			p.Reg = new(RegisterWiFiCommMode)
		case QuickChargerStatusRegister, ChargerInhibitRegister:
			p.Reg = new(RegisterChargerSubStatus)
		case PreACStateRegister:
			p.Reg = new(RegisterPreACState)
		case TimeRegister:
			p.Reg = new(RegisterTime)
		case TheftAlarmEventRegister:
			p.Reg = new(RegisterTheftAlarmEvent)
		case VINRegister:
			p.Reg = new(RegisterVIN)
		case SettingsRegister:
			p.Reg = new(RegisterSettings)
		case ChargeTimerOverrideReg, ChargeTimerStateRegister:
			p.Reg = new(RegisterChargeTimerState)
		case ExternalPowerRegister:
			if len(p.Data) == 4 {
				p.Reg = new(RegisterExternalPower)
			} else {
				p.Reg = new(RegisterGeneric)
			}
		case ACOperStatusRegister:
			p.Reg = new(RegisterACOperStatus)
		case ACModeRegister:
			p.Reg = new(RegisterACMode)
		case BatteryLevelRegister:
			p.Reg = new(RegisterBatteryLevel)
		case ChargePlugRegister:
			p.Reg = new(RegisterChargePlug)
		case ChargeStatusRegister:
			p.Reg = new(RegisterChargeStatus)
		case LightStatusRegister:
			p.Reg = new(RegisterLightStatus)
		case DoorStatusRegister:
			p.Reg = new(RegisterDoorStatus)
		case WiFiAnyConnRegister:
			p.Reg = new(RegisterWiFiAnyConn)
		case WIFISSIDRegister:
			p.Reg = new(RegisterWIFISSID)
		case VehicleVariantRegister:
			p.Reg = new(RegisterVehicleVariant)
		case ECUVersionShortRegister, ECUVersionRegister:
			p.Reg = new(RegisterECUVersion)
		default:
			p.Reg = new(RegisterGeneric)
		}
		p.Reg.Decode(p)
	}
	return nil
}

func (p *PhevMessage) DecodeFromBytes(data []byte, key *SecurityKey) error {
	if len(data) < 4 {
		return fmt.Errorf("invalid packet length")
	}
	decoded, xor, _ := ValidateAndDecodeMessage(data)
	if len(decoded) == 0 {
		return fmt.Errorf("failed to validate packet checksum")
	}
	return p.populateDecoded(decoded, data, xor, key)
}

func (p *PhevMessage) String() string {
	return fmt.Sprintf(
		`Cmd: 0x%x (%s) (len %d), Register 0x%x, Data: %s`,
		p.Type, messageStr[p.Type], p.Length, p.Register, hex.EncodeToString(p.Data))
}

func NewFromBytes(data []byte, key *SecurityKey) []*PhevMessage {
	msgs := []*PhevMessage{}

	log.Tracef("%%PHEV_DECODE_FROM_BYTES%%: Raw: %s", hex.EncodeToString(data))
	offset := 0
	for len(data) >= 6 {
		if offset > len(data)-6 {
			break
		}
		dat, xor, rem := ValidateAndDecodeMessage(data[offset:])
		if len(dat) == 0 || (offset > 0 && !isKnownPacketType(dat[0])) {
			offset++
			continue
		}
		log.Tracef("%%PHEV_DECODED_FROM_BYTES%%: Raw: %s", hex.EncodeToString(dat))
		origXored := data[offset : offset+len(dat)]
		p := &PhevMessage{}
		if err := p.populateDecoded(dat, origXored, xor, key); err != nil {
			log.Errorf("decode error: %v", err)
			break
		}
		msgs = append(msgs, p)
		if len(rem) < 6 {
			break
		}
		data = rem
		offset = 0
	}
	return msgs
}

func encodeTime(t time.Time) []byte {
	return []byte{
		byte(t.Year() - 2000),
		byte(t.Month()),
		byte(t.Day()),
		byte(t.Hour()),
		byte(t.Minute()),
		byte(t.Second()),
		byte(t.Weekday()),
	}
}

// EncodeSyncTime encodes a timestamp into the 8-byte payload expected by write register 0x05.
func EncodeSyncTime(t time.Time) []byte {
	out := make([]byte, 8)
	copy(out, encodeTime(t))
	out[7] = 0x00 // normal (non-rooted) client flag
	return out
}

func decodeTime(m []byte) time.Time {
	month := int(m[1])
	if month < 1 || month > 12 {
		month = 1
	}
	day := int(m[2])
	if day < 1 || day > 31 {
		day = 1
	}
	return time.Date(
		2000+int(m[0]),
		time.Month(month),
		day,
		int(m[3]),
		int(m[4]),
		int(m[5]),
		0, time.Local)
}

const (
	// Read registers (0x6f)
	PreACNotificationRegister  = 0x01
	BatteryWarningRegister     = 0x02
	CapabilityRegister         = 0x03
	VINAltRegister             = 0x06
	WiFiChannelRegister        = 0x07
	WiFiPasswordResultRegister = 0x08
	WiFiCommModeRegister       = 0x0b
	QuickChargerStatusRegister = 0x0c
	ChargerInhibitRegister     = 0x0d
	PreACStateRegister         = 0x10
	TimeRegister               = 0x12
	CommStartNotifyRegister    = 0x13
	TheftAlarmEventRegister    = 0x14
	VINRegister                = 0x15
	SettingsRegister           = 0x16
	ExternalPowerRegister      = 0x18
	ACOperStatusRegister       = 0x1a
	ACModeRegister             = 0x1c
	BatteryLevelRegister       = 0x1d
	ChargePlugRegister         = 0x1e
	ChargeStatusRegister       = 0x1f
	LightStatusRegister        = 0x23
	DoorStatusRegister         = 0x24
	WiFiAnyConnRegister        = 0x27
	WIFISSIDRegister           = 0x28
	VehicleVariantRegister     = 0x29
	RegistrationStateRegister  = 0x2a
	ECUVersionShortRegister    = 0x2b
	RegistrationCancelRegister = 0x2c
	ReadyBeaconRegister        = 0xaa
	ECUVersionRegister         = 0xc0

	// Write registers (0xf6)
	SetACModeRegisterMY14       = 0x02
	SetACEnabledRegisterMY14    = 0x04
	SetSyncTimeRegister         = 0x05
	SetRequestUpdateRegister    = 0x06
	SetAckCommNotifyRegister    = 0x07
	SetAckTheftAlarmRegister    = 0x08
	SetHeadlightsRegister       = 0x0a
	SetParkingLightsRegister    = 0x0b
	SetRegisterClientRegister   = 0x10
	SetWiFiAlwaysOnRegister     = 0x11
	SetAckChargeErrorRegister   = 0x12
	SetAckPreACTermRegister     = 0x13
	SetChangeSSIDRegister       = 0x14
	SetUnregisterClientRegister = 0x15
	SetWiFiCommModeRegister     = 0x18
	SetACModeRegisterMY18       = 0x1b
	SetWiFiChannelRegister      = 0x1c
	SetWiFiPasswordRegister     = 0x1d
	SetCommitWiFiPasswordReg    = 0x1e
)

type Register interface {
	Decode(*PhevMessage)
	Encode() *PhevMessage
	Raw() string
	String() string
	Register() byte
}

type RegisterGeneric struct {
	Reg   byte
	Value []byte
}

func (r *RegisterGeneric) Decode(m *PhevMessage) {
	r.Reg = m.Register
	r.Value = m.Data
}

func (r *RegisterGeneric) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.Value,
	}
}

func (r *RegisterGeneric) Raw() string {
	return hex.EncodeToString(r.Value)
}

func (r *RegisterGeneric) String() string {
	return fmt.Sprintf("g(0x%02x): %s", r.Reg, r.Raw())
}

func (r *RegisterGeneric) Register() byte {
	return r.Reg
}

// RegisterPreACNotification decodes register 0x01 (Remote AC capability & stop flag).
type RegisterPreACNotification struct {
	Capable    bool
	StopReason byte
	raw        []byte
}

func (r *RegisterPreACNotification) Decode(m *PhevMessage) {
	if len(m.Data) < 2 {
		return
	}
	r.Capable = m.Data[0] != 0
	r.StopReason = m.Data[1]
	r.raw = m.Data
}

func (r *RegisterPreACNotification) Encode() *PhevMessage {
	var capByte byte
	if r.Capable {
		capByte = 1
	}
	return &PhevMessage{Register: r.Register(), Data: []byte{capByte, r.StopReason}}
}

func (r *RegisterPreACNotification) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterPreACNotification) String() string {
	return fmt.Sprintf("Pre-AC Capable: %t, StopFlag: %d", r.Capable, r.StopReason)
}
func (r *RegisterPreACNotification) Register() byte { return PreACNotificationRegister }

// RegisterCapability decodes register 0x03 (ECU Security & Feature Capability Bitmask).
type RegisterCapability struct {
	SecurityEquipped             bool
	WPAPasswordChangeSupported   bool
	ExternalPowerSupplySupported bool
	raw                          []byte
}

func (r *RegisterCapability) Decode(m *PhevMessage) {
	if len(m.Data) < 3 {
		return
	}
	r.SecurityEquipped = m.Data[0] != 0
	r.WPAPasswordChangeSupported = (m.Data[1] & 0x04) != 0
	r.ExternalPowerSupplySupported = (m.Data[2] & 0x10) != 0
	r.raw = m.Data
}

func (r *RegisterCapability) Encode() *PhevMessage {
	data := make([]byte, 3)
	if len(r.raw) == 3 {
		copy(data, r.raw)
	} else {
		if r.SecurityEquipped {
			data[0] = 0x01
		}
		if r.WPAPasswordChangeSupported {
			data[1] |= 0x04
		}
		if r.ExternalPowerSupplySupported {
			data[2] |= 0x10
		}
	}
	return &PhevMessage{Register: r.Register(), Data: data}
}

func (r *RegisterCapability) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterCapability) String() string {
	return fmt.Sprintf("Capabilities: security=%t wpa_change=%t v2h=%t",
		r.SecurityEquipped, r.WPAPasswordChangeSupported, r.ExternalPowerSupplySupported)
}
func (r *RegisterCapability) Register() byte { return CapabilityRegister }

// RegisterChargeTimers decodes register 0x04 on MY18+ (20 bytes: 5 x 4B charge timers).
type RegisterChargeTimers struct {
	Schedule ChargeSchedule
	raw      []byte
}

func (r *RegisterChargeTimers) Decode(m *PhevMessage) {
	if len(m.Data) != 20 {
		return
	}
	_ = r.Schedule.DecodeRead0x04(m.Data)
	r.raw = m.Data
}

func (r *RegisterChargeTimers) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.Schedule.EncodeRead0x04(),
	}
}

func (r *RegisterChargeTimers) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterChargeTimers) String() string {
	parts := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		parts = append(parts, r.Schedule.Timers[i].Summary())
	}
	return "Charge Timers: " + strings.Join(parts, " | ")
}
func (r *RegisterChargeTimers) Register() byte { return ChargeTimersRegister }

// RegisterClimateTimers decodes register 0x05 on MY18+ (16 bytes: master enable + 5 x 3B climate timers).
type RegisterClimateTimers struct {
	Schedule ClimateSchedule
	raw      []byte
}

func (r *RegisterClimateTimers) Decode(m *PhevMessage) {
	if len(m.Data) != 16 {
		return
	}
	_ = r.Schedule.DecodeRead0x05(m.Data)
	r.raw = m.Data
}

func (r *RegisterClimateTimers) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.Schedule.EncodeRead0x05(),
	}
}

func (r *RegisterClimateTimers) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterClimateTimers) String() string {
	parts := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		parts = append(parts, r.Schedule.Timers[i].Summary())
	}
	return fmt.Sprintf("Climate Timers (master=%t): %s", r.Schedule.MasterEnabled, strings.Join(parts, " | "))
}
func (r *RegisterClimateTimers) Register() byte { return ClimateTimersRegister }

// RegisterWiFiChannel decodes register 0x07 (Wi-Fi AP channel 1..13).
type RegisterWiFiChannel struct {
	Channel int
	raw     []byte
}

func (r *RegisterWiFiChannel) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.Channel = int(m.Data[0])
	r.raw = m.Data
}

func (r *RegisterWiFiChannel) Encode() *PhevMessage {
	return &PhevMessage{Register: r.Register(), Data: []byte{byte(r.Channel)}}
}
func (r *RegisterWiFiChannel) Raw() string      { return hex.EncodeToString(r.raw) }
func (r *RegisterWiFiChannel) String() string   { return fmt.Sprintf("WiFi Channel: %d", r.Channel) }
func (r *RegisterWiFiChannel) Register() byte   { return WiFiChannelRegister }

// RegisterWiFiCommMode decodes register 0x0b (Wireless LAN communication mode).
type RegisterWiFiCommMode struct {
	Mode byte
	raw  []byte
}

func (r *RegisterWiFiCommMode) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.Mode = m.Data[0]
	r.raw = m.Data
}

func (r *RegisterWiFiCommMode) Encode() *PhevMessage {
	return &PhevMessage{Register: r.Register(), Data: []byte{r.Mode}}
}
func (r *RegisterWiFiCommMode) Raw() string    { return hex.EncodeToString(r.raw) }
func (r *RegisterWiFiCommMode) String() string { return fmt.Sprintf("WiFi Comm Mode: %d", r.Mode) }
func (r *RegisterWiFiCommMode) Register() byte { return WiFiCommModeRegister }

// RegisterChargerSubStatus decodes registers 0x0c and 0x0d (Quick charger & timer inhibition flags).
type RegisterChargerSubStatus struct {
	Reg            byte
	Status         byte
	QuickCharger   bool
	TimerInhibited bool
	raw            []byte
}

func (r *RegisterChargerSubStatus) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.Reg = m.Register
	r.Status = m.Data[0]
	if m.Register == QuickChargerStatusRegister {
		r.QuickCharger = m.Data[0] == 3
	}
	if m.Register == ChargerInhibitRegister {
		r.TimerInhibited = m.Data[0] == 1
	}
	r.raw = m.Data
}

func (r *RegisterChargerSubStatus) Encode() *PhevMessage {
	return &PhevMessage{Register: r.Reg, Data: []byte{r.Status}}
}
func (r *RegisterChargerSubStatus) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterChargerSubStatus) String() string {
	if r.Reg == ChargerInhibitRegister {
		return fmt.Sprintf("Charger Timer Inhibited: %t (%d)", r.TimerInhibited, r.Status)
	}
	return fmt.Sprintf("Quick Charger Status: %t (%d)", r.QuickCharger, r.Status)
}
func (r *RegisterChargerSubStatus) Register() byte { return r.Reg }

type RegisterTime struct {
	Time time.Time
	raw  []byte
}

func (r *RegisterTime) Decode(m *PhevMessage) {
	if len(m.Data) != 7 {
		return
	}
	r.Time = decodeTime(m.Data)
	r.raw = m.Data
}

func (r *RegisterTime) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     encodeTime(r.Time),
	}
}

func (r *RegisterTime) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterTime) String() string {
	return r.Time.Format("2006-01-02 15:04:05")
}

func (r *RegisterTime) Register() byte {
	return TimeRegister
}

// RegisterTheftAlarmEvent decodes register 0x14 (7B: 6B timestamp + 1B alarm trigger cause).
type RegisterTheftAlarmEvent struct {
	Time             time.Time
	ValidTime        bool
	CauseCode        byte
	Cause            string
	CauseDescription string
	raw              []byte
}

func (r *RegisterTheftAlarmEvent) Decode(m *PhevMessage) {
	if len(m.Data) != 7 {
		return
	}
	r.raw = m.Data
	r.CauseCode = m.Data[6]
	if m.Data[1] >= 1 && m.Data[1] <= 12 && m.Data[2] >= 1 && m.Data[2] <= 31 {
		r.Time = decodeTime(m.Data[:6])
		r.ValidTime = true
	}
	switch r.CauseCode {
	case 0:
		r.Cause = "none"
		r.CauseDescription = "None"
	case 1:
		r.Cause = "intrusion"
		r.CauseDescription = "Unauthorized intrusion"
	case 2:
		r.Cause = "movement"
		r.CauseDescription = "Illegal vehicle movement"
	case 3:
		r.Cause = "door_opened"
		r.CauseDescription = "Door opened illegally"
	case 4:
		r.Cause = "illegal_key"
		r.CauseDescription = "Illegal key used"
	case 5:
		r.Cause = "vibration"
		r.CauseDescription = "Unauthorized vibration"
	case 6:
		r.Cause = "illegal_access"
		r.CauseDescription = "Illegal access"
	default:
		r.Cause = fmt.Sprintf("code_%d", r.CauseCode)
		r.CauseDescription = fmt.Sprintf("Alarm code %d", r.CauseCode)
	}
}

func (r *RegisterTheftAlarmEvent) Encode() *PhevMessage {
	data := make([]byte, 7)
	if len(r.raw) == 7 {
		copy(data, r.raw)
	} else {
		if r.ValidTime {
			copy(data[:6], encodeTime(r.Time)[:6])
		}
		data[6] = r.CauseCode
	}
	return &PhevMessage{Register: r.Register(), Data: data}
}

func (r *RegisterTheftAlarmEvent) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterTheftAlarmEvent) String() string {
	if r.CauseCode == 0 {
		return "Theft Alarm Event: none"
	}
	if r.ValidTime {
		return fmt.Sprintf("Theft Alarm Event: %s at %s", r.CauseDescription, r.Time.Format("2006-01-02 15:04:05"))
	}
	return fmt.Sprintf("Theft Alarm Event: %s", r.CauseDescription)
}
func (r *RegisterTheftAlarmEvent) Register() byte { return TheftAlarmEventRegister }

type RegisterSettings struct {
	register byte
	Items    []*VehicleSetting
	raw      []byte
}

func (r *RegisterSettings) Decode(m *PhevMessage) {
	r.register = m.Register
	r.raw = m.Data
	if items, err := DecodeSettingsFrame(m.Data); err == nil {
		r.Items = items
	}
}

func (r *RegisterSettings) Encode() *PhevMessage {
	return &PhevMessage{
		Register: r.Register(),
		Data:     r.raw,
	}
}

func (r *RegisterSettings) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterSettings) String() string {
	if len(r.raw) != 8 {
		return fmt.Sprintf("Car Settings: %s", r.Raw())
	}
	value := binary.LittleEndian.Uint64(r.raw)
	parts := []string{}
	for _, it := range r.Items {
		if it.Supported() {
			parts = append(parts, fmt.Sprintf("0x%02x %s=%s", it.ID, it.Slug, it.ValueName()))
		}
	}
	if len(parts) > 0 {
		return fmt.Sprintf("Car Settings: %016x (%s)", value, strings.Join(parts, "; "))
	}
	return fmt.Sprintf("Car Settings: %016x", value)
}

func (r *RegisterSettings) Register() byte {
	if r.register == 0 {
		return SettingsRegister
	}
	return r.register
}

type RegisterVIN struct {
	VIN           string // Preserved 16-char prefix for HA unique_id backward compatibility
	FullVIN       string // Full 17-character ISO VIN
	Registrations int
	reg           byte
	raw           []byte
}

func (r *RegisterVIN) Decode(m *PhevMessage) {
	if (m.Register != VINRegister && m.Register != VINAltRegister) || len(m.Data) != 20 {
		return
	}
	r.reg = m.Register
	r.VIN = strings.TrimRight(string(m.Data[1:17]), "\x00")
	r.FullVIN = strings.TrimRight(string(m.Data[1:18]), "\x00")
	r.Registrations = int(m.Data[19])
	r.raw = m.Data
}

func (r *RegisterVIN) Encode() *PhevMessage {
	data := make([]byte, 20)
	data[0] = 0x01
	vinSrc := r.FullVIN
	if vinSrc == "" {
		vinSrc = r.VIN
	}
	if len(vinSrc) > 17 {
		vinSrc = vinSrc[:17]
	}
	copy(data[1:18], []byte(vinSrc))
	data[19] = byte(r.Registrations)
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterVIN) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterVIN) String() string {
	v := r.FullVIN
	if v == "" {
		v = r.VIN
	}
	return fmt.Sprintf("VIN: %s Registrations: %d", v, r.Registrations)
}

func (r *RegisterVIN) Register() byte {
	if r.reg != 0 {
		return r.reg
	}
	return VINRegister
}

// RegisterChargeTimerState decodes registers 0x1b (Master Charge Timer Enable) and 0x17.
type RegisterChargeTimerState struct {
	Reg       byte
	Enabled   bool
	Cancelled bool
	raw       []byte
}

func (r *RegisterChargeTimerState) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.Reg = m.Register
	low := m.Data[0] & 0x0f
	r.Enabled = low >= 2
	r.Cancelled = low == 1
	r.raw = m.Data
}

func (r *RegisterChargeTimerState) Encode() *PhevMessage {
	var b byte = 0x01
	if r.Enabled {
		b = 0x02
	}
	reg := r.Reg
	if reg == 0 {
		reg = ChargeTimerStateRegister
	}
	return &PhevMessage{Register: reg, Data: []byte{b}}
}

func (r *RegisterChargeTimerState) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterChargeTimerState) String() string {
	return fmt.Sprintf("Charge Timer Enabled: %t", r.Enabled)
}
func (r *RegisterChargeTimerState) Register() byte {
	if r.Reg == 0 {
		return ChargeTimerStateRegister
	}
	return r.Reg
}

// RegisterExternalPower decodes register 0x18 on MY18+ (4B: V2H / V2L External Power Supply status).
type RegisterExternalPower struct {
	State  byte
	Error  byte
	Active bool
	raw    []byte
}

func (r *RegisterExternalPower) Decode(m *PhevMessage) {
	if len(m.Data) != 4 {
		return
	}
	r.State = m.Data[0]
	r.Error = m.Data[1]
	r.Active = m.Data[0] != 0
	r.raw = m.Data
}

func (r *RegisterExternalPower) Encode() *PhevMessage {
	data := make([]byte, 4)
	if len(r.raw) == 4 {
		copy(data, r.raw)
	} else {
		data[0] = r.State
		data[1] = r.Error
	}
	return &PhevMessage{Register: r.Register(), Data: data}
}
func (r *RegisterExternalPower) Raw() string { return hex.EncodeToString(r.raw) }
func (r *RegisterExternalPower) String() string {
	return fmt.Sprintf("External Power Supply: active=%t state=%d err=%d", r.Active, r.State, r.Error)
}
func (r *RegisterExternalPower) Register() byte { return ExternalPowerRegister }

type RegisterECUVersion struct {
	Version    string
	SubVersion string
	reg        byte
	raw        []byte
}

func (r *RegisterECUVersion) Decode(m *PhevMessage) {
	if len(m.Data) < 10 {
		return
	}
	r.reg = m.Register
	r.Version = strings.TrimSpace(strings.TrimRight(string(m.Data[:10]), "\x00"))
	if len(m.Data) >= 12 {
		r.SubVersion = hex.EncodeToString(m.Data[10:12])
	}
	r.raw = m.Data
}

func (r *RegisterECUVersion) Encode() *PhevMessage {
	if r.reg == ECUVersionShortRegister {
		data := make([]byte, 10)
		copy(data, []byte(r.Version))
		return &PhevMessage{Register: ECUVersionShortRegister, Data: data}
	}
	data := make([]byte, 13)
	copy(data[:10], []byte(r.Version))
	data[10] = 0x10
	data[11] = 0x01
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterECUVersion) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterECUVersion) String() string {
	if r.SubVersion != "" {
		return fmt.Sprintf("ECU Version: %s (%s)", r.Version, r.SubVersion)
	}
	return fmt.Sprintf("ECU Version: %s", r.Version)
}

func (r *RegisterECUVersion) Register() byte {
	if r.reg != 0 {
		return r.reg
	}
	return ECUVersionRegister
}

type RegisterBatteryLevel struct {
	Level                int
	Valid                bool
	SubStatus            byte
	ParkingLights        bool // yes parking lights here.
	LightControlDisabled bool
	raw                  []byte
}

func (r *RegisterBatteryLevel) Decode(m *PhevMessage) {
	if m.Register != BatteryLevelRegister || len(m.Data) != 4 {
		return
	}
	r.Level = int(m.Data[0])
	r.Valid = m.Data[0] <= 100
	r.SubStatus = m.Data[1]
	r.ParkingLights = m.Data[2] == 0x1
	r.LightControlDisabled = m.Data[3] != 0
	r.raw = m.Data
}

func (r *RegisterBatteryLevel) Encode() *PhevMessage {
	data := []byte{byte(r.Level), r.SubStatus, 0x0, 0x0}
	if r.ParkingLights {
		data[2] = 0x1
	}
	if r.LightControlDisabled {
		data[3] = 0x1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterBatteryLevel) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterBatteryLevel) String() string {
	return fmt.Sprintf("Battery level: %d%% (parking_lights=%t)", r.Level, r.ParkingLights)
}

func (r *RegisterBatteryLevel) Register() byte {
	return BatteryLevelRegister
}

type RegisterBatteryWarning struct {
	Warning       int
	WarningCode   byte
	WarmingActive bool
	WarningText   string
	GunStatus     byte
	PlugStatus    byte
	raw           []byte
}

func (r *RegisterBatteryWarning) Decode(m *PhevMessage) {
	if m.Register != BatteryWarningRegister || len(m.Data) != 4 {
		return
	}
	r.GunStatus = m.Data[0]
	r.PlugStatus = m.Data[1]
	r.Warning = int(m.Data[2])
	r.WarningCode = m.Data[2] & 0x0f
	r.WarmingActive = (m.Data[2] & 0xf0) != 0
	switch r.WarningCode {
	case 0:
		if r.WarmingActive {
			r.WarningText = "warming_up"
		} else {
			r.WarningText = "normal"
		}
	case 1:
		r.WarningText = "low_temp_plug_in_reminder"
	case 2:
		r.WarningText = "warming_stopped_unplugged"
	case 3:
		r.WarningText = "extreme_cold_cannot_charge"
	case 4:
		r.WarningText = "very_cold_reduced_output"
	case 5:
		r.WarningText = "warming_stopping_low_soc"
	case 6:
		r.WarningText = "warming_stopped_low_soc"
	default:
		r.WarningText = fmt.Sprintf("warning_%d", r.WarningCode)
	}
	r.raw = m.Data
}

func (r *RegisterBatteryWarning) Encode() *PhevMessage {
	data := []byte{r.GunStatus, r.PlugStatus, byte(r.Warning), 0x0}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterBatteryWarning) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterBatteryWarning) String() string {
	return fmt.Sprintf("Battery warning: %s (%d)", r.WarningText, r.Warning)
}

func (r *RegisterBatteryWarning) Register() byte {
	return BatteryWarningRegister
}

type RegisterDoorStatus struct {
	// Locked is true if the vehicle is locked.
	Locked bool
	// The below are true if the corresponding door is open.
	Driver, FrontPassenger, RearLeft, RearRight bool
	Bonnet, Boot                                bool
	// Headlight state is in this register!
	Headlights bool
	raw        []byte
}

func (r *RegisterDoorStatus) Decode(m *PhevMessage) {
	if m.Register != DoorStatusRegister || len(m.Data) != 10 {
		return
	}
	r.Locked = m.Data[0] == 0x1
	r.Driver = m.Data[3] == 0x1
	r.FrontPassenger = m.Data[4] == 0x1
	r.RearRight = m.Data[5] == 0x1
	r.RearLeft = m.Data[6] == 0x1
	r.Boot = m.Data[7] == 0x1
	r.Bonnet = m.Data[8] == 0x1
	r.Headlights = m.Data[9] == 0x1
	r.raw = m.Data
}

func (r *RegisterDoorStatus) Encode() *PhevMessage {
	data := make([]byte, 10)
	if r.Locked {
		data[0] = 0x1
	} else {
		data[0] = 0x2
	}
	if r.Driver {
		data[3] = 0x1
	}
	if r.FrontPassenger {
		data[4] = 0x1
	}
	if r.RearRight {
		data[5] = 0x1
	}
	if r.RearLeft {
		data[6] = 0x1
	}
	if r.Boot {
		data[7] = 0x1
	}
	if r.Bonnet {
		data[8] = 0x1
	}
	if r.Headlights {
		data[9] = 0x1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterDoorStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterDoorStatus) String() string {
	openStr := ""
	if r.FrontPassenger || r.Driver || r.RearRight || r.RearLeft || r.Boot || r.Bonnet {
		openStr = " Open:"
		if r.Driver {
			openStr += " driver"
		}
		if r.FrontPassenger {
			openStr += " front_passenger"
		}
		if r.RearRight {
			openStr += " rear_right"
		}
		if r.RearLeft {
			openStr += " rear_left"
		}
		if r.Bonnet {
			openStr += " bonnet"
		}
		if r.Boot {
			openStr += " boot"
		}
	}
	if r.Locked {
		return "Doors locked." + openStr
	}
	return "Doors unlocked." + openStr
}

func (r *RegisterDoorStatus) Register() byte {
	return DoorStatusRegister
}

type RegisterChargeStatus struct {
	Charging   bool
	ChargeType string // "off", "normal", "quick"
	Remaining  int    // minutes
	raw        []byte
}

func (r *RegisterChargeStatus) Decode(m *PhevMessage) {
	if m.Register != ChargeStatusRegister || len(m.Data) != 3 {
		return
	}
	switch m.Data[0] {
	case 0:
		r.Charging = false
		r.ChargeType = "off"
	case 1:
		r.Charging = true
		r.ChargeType = "normal"
	case 2, 4, 5:
		r.Charging = true
		r.ChargeType = "quick"
	default:
		r.Charging = m.Data[0] > 0
		r.ChargeType = "normal"
	}
	r.Remaining = 0
	if m.Data[2] != 0xff {
		r.Remaining = int(m.Data[2])<<8 | int(m.Data[1])
	}
	r.raw = m.Data
}

func (r *RegisterChargeStatus) Encode() *PhevMessage {
	data := []byte{0x00, 0xff, 0xff}
	if r.Charging {
		if r.ChargeType == "quick" {
			data[0] = 0x02
		} else {
			data[0] = 0x01
		}
		data[1] = byte(r.Remaining % 256)
		data[2] = byte(r.Remaining / 256)
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterChargeStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterChargeStatus) String() string {
	if r.Charging {
		return fmt.Sprintf("Charging (%s), %d min remaining.", r.ChargeType, r.Remaining)
	}
	return "Not charging"
}

func (r *RegisterChargeStatus) Register() byte {
	return ChargeStatusRegister
}

type PreACState int8

const (
	PreACOff        PreACState = 0
	PreACStarting   PreACState = 1
	PreACOn         PreACState = 2
	PreACTerminated PreACState = 3
)

type RegisterPreACState struct {
	State         PreACState
	HasTargetTime bool
	TargetDay     byte
	TargetHour    int
	TargetMinute  int
	raw           []byte
}

func (r *RegisterPreACState) Encode() *PhevMessage {
	data := make([]byte, 3)
	data[0] = byte(r.State)
	if r.HasTargetTime {
		data[1] = ((r.TargetDay & 0x07) << 5) | byte(r.TargetHour&0x1f)
		data[2] = byte(r.TargetMinute)
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterPreACState) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.State = PreACState(m.Data[0])
	if len(m.Data) >= 3 && (m.Data[1] != 0 || m.Data[2] != 0) {
		hr := int(m.Data[1] & 0x1f)
		mn := int(m.Data[2])
		if hr <= 23 && mn <= 59 {
			r.HasTargetTime = true
			r.TargetDay = (m.Data[1] >> 5) & 0x07
			r.TargetHour = hr
			r.TargetMinute = mn
		}
	}
	r.raw = m.Data
}

func (r *RegisterPreACState) TargetTimeString() string {
	if !r.HasTargetTime {
		return "--:--"
	}
	return fmt.Sprintf("%02d:%02d", r.TargetHour, r.TargetMinute)
}

func (r *RegisterPreACState) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterPreACState) String() string {
	switch r.State {
	case PreACOff:
		return "Pre-AC off"
	case PreACStarting:
		return "Pre-AC starting"
	case PreACOn:
		if r.HasTargetTime {
			return fmt.Sprintf("Pre-AC on (until %s)", r.TargetTimeString())
		}
		return "Pre-AC on"
	case PreACTerminated:
		return "Pre-AC terminated (door opened or battery low)"
	default:
		return fmt.Sprintf("Pre-AC: unknown (%v)", r.State)
	}
}

func (r *RegisterPreACState) Register() byte {
	return PreACStateRegister
}

type RegisterACOperStatus struct {
	IgnitionRaw  byte
	Ignition     string // "off", "acc", "on", "ready"
	IgnitionOn   bool
	Operating    bool
	Duration     int  // 10, 20, or 30 minutes (MY18+)
	Delay        int  // 0, 5, or 10 minutes (MY18+)
	DelayPending bool // true when waiting for delayed start
	raw          []byte
}

func (r *RegisterACOperStatus) Decode(m *PhevMessage) {
	if m.Register != ACOperStatusRegister || len(m.Data) < 2 {
		return
	}
	r.IgnitionRaw = m.Data[0]
	switch m.Data[0] {
	case 0:
		r.Ignition = "off"
		r.IgnitionOn = false
	case 1, 2, 3:
		r.Ignition = "acc"
		r.IgnitionOn = true
	case 4:
		r.Ignition = "on"
		r.IgnitionOn = true
	case 7:
		r.Ignition = "ready"
		r.IgnitionOn = true
	default:
		r.Ignition = "on"
		r.IgnitionOn = true
	}
	r.Operating = m.Data[1] == 1
	if len(m.Data) >= 5 {
		switch m.Data[2] {
		case 0:
			r.Duration = 10
		case 1:
			r.Duration = 20
		case 2:
			r.Duration = 30
		default:
			r.Duration = 10
		}
		switch m.Data[3] {
		case 1:
			r.Delay = 5
		case 2:
			r.Delay = 10
		default:
			r.Delay = 0
		}
		r.DelayPending = m.Data[4] == 1
	}
	r.raw = m.Data
}

func (r *RegisterACOperStatus) Encode() *PhevMessage {
	data := make([]byte, 5)
	data[0] = r.IgnitionRaw
	if data[0] == 0 && r.IgnitionOn {
		switch r.Ignition {
		case "acc":
			data[0] = 3
		case "ready":
			data[0] = 7
		default:
			data[0] = 4
		}
	}
	if r.Operating {
		data[1] = 0x1
	}
	switch r.Duration {
	case 20:
		data[2] = 1
	case 30:
		data[2] = 2
	}
	switch r.Delay {
	case 5:
		data[3] = 1
	case 10:
		data[3] = 2
	}
	if r.DelayPending {
		data[4] = 1
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterACOperStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterACOperStatus) String() string {
	ac := "off"
	if r.Operating {
		ac = "on"
	} else if r.DelayPending {
		ac = fmt.Sprintf("delayed (%dm)", r.Delay)
	}
	return fmt.Sprintf("Ignition: %s, AC: %s", r.Ignition, ac)
}

func (r *RegisterACOperStatus) Register() byte {
	return ACOperStatusRegister
}

type RegisterACMode struct {
	Mode     string
	Duration uint8
	raw      []byte
}

func (r *RegisterACMode) Decode(m *PhevMessage) {
	if len(m.Data) != 1 {
		return
	}
	switch m.Data[0] & 0x0f {
	case 0:
		r.Mode = "unknown"
	case 1:
		r.Mode = "cool"
	case 2:
		r.Mode = "heat"
	case 3:
		r.Mode = "windscreen"
	default:
		r.Mode = "unknown"
	}
	switch m.Data[0] & 0xf0 {
	case 0x00:
		r.Duration = 10
	case 0x10:
		r.Duration = 20
	case 0x20:
		r.Duration = 30
	default:
		r.Duration = 10
	}
	r.raw = m.Data
}

func (r *RegisterACMode) Encode() *PhevMessage {
	var data byte
	switch r.Mode {
	case "cool":
		data = 0x1
	case "heat":
		data = 0x2
	case "windscreen":
		data = 0x3
	default:
		data = 0x0
	}
	switch r.Duration {
	case 20:
		data |= 0x10
	case 30:
		data |= 0x20
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     []byte{data},
	}
}

func (r *RegisterACMode) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterACMode) String() string {
	return r.Mode
}

func (r *RegisterACMode) Register() byte {
	return ACModeRegister
}

type RegisterChargePlug struct {
	Connected       bool
	WaitingForTimer bool
	PlugState       string // "unplugged", "connected_waiting", "connected", "charging"
	raw             []byte
}

func (r *RegisterChargePlug) Decode(m *PhevMessage) {
	if len(m.Data) != 2 {
		return
	}
	// m.Data[1]: 0/3 = unplugged, 1 = plugged in (timer inhibited), 2 = plugged in (ready/charging)
	// m.Data[0]: 0 = idle, 1/2 = active power transfer
	r.Connected = (m.Data[1] == 1 || m.Data[1] == 2 || m.Data[0] > 0)
	r.WaitingForTimer = (m.Data[1] == 1 && m.Data[0] == 0)
	switch {
	case !r.Connected:
		r.PlugState = "unplugged"
	case m.Data[0] > 0:
		r.PlugState = "charging"
	case m.Data[1] == 1:
		r.PlugState = "connected_waiting"
	default:
		r.PlugState = "connected"
	}
	r.raw = m.Data
}

func (r *RegisterChargePlug) Encode() *PhevMessage {
	data := make([]byte, 2)
	if r.Connected {
		if r.PlugState == "charging" {
			data[0] = 0x2
			data[1] = 0x2
		} else if r.WaitingForTimer || r.PlugState == "connected_waiting" {
			data[1] = 0x1
		} else {
			data[1] = 0x2
		}
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterChargePlug) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterChargePlug) String() string {
	if r.Connected {
		return fmt.Sprintf("Charger connected (%s)", r.PlugState)
	}
	return "Charger disconnected"
}

func (r *RegisterChargePlug) Register() byte {
	return ChargePlugRegister
}

// RegisterWiFiAnyConn decodes register 0x27 (Always-Communicate Wi-Fi mode).
type RegisterWiFiAnyConn struct {
	AlwaysOn bool
	raw      []byte
}

func (r *RegisterWiFiAnyConn) Decode(m *PhevMessage) {
	if len(m.Data) < 1 {
		return
	}
	r.AlwaysOn = m.Data[0] == 1
	r.raw = m.Data
}

func (r *RegisterWiFiAnyConn) Encode() *PhevMessage {
	var b byte
	if r.AlwaysOn {
		b = 1
	}
	return &PhevMessage{Register: r.Register(), Data: []byte{b}}
}
func (r *RegisterWiFiAnyConn) Raw() string    { return hex.EncodeToString(r.raw) }
func (r *RegisterWiFiAnyConn) String() string { return fmt.Sprintf("WiFi Always On: %t", r.AlwaysOn) }
func (r *RegisterWiFiAnyConn) Register() byte { return WiFiAnyConnRegister }

type RegisterWIFISSID struct {
	SSID string
	raw  []byte
}

func (r *RegisterWIFISSID) Decode(m *PhevMessage) {
	if m.Register != WIFISSIDRegister || len(m.Data) != 32 {
		return
	}
	r.raw = append([]byte(nil), m.Data...)
	dat := append([]byte(nil), m.Data...)
	for i, b := range dat {
		if b == 0xff {
			dat[i] = 0x0
		}
	}
	r.SSID = strings.TrimRight(string(dat), "\x00")
}

func (r *RegisterWIFISSID) Encode() *PhevMessage {
	buf := make([]byte, 32)
	src := []byte(r.SSID)
	if len(src) > 32 {
		src = src[:32]
	}
	copy(buf, src)
	return &PhevMessage{
		Register: r.Register(),
		Data:     buf,
	}
}

func (r *RegisterWIFISSID) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterWIFISSID) String() string {
	return fmt.Sprintf("SSID: %s", r.SSID)
}

func (r *RegisterWIFISSID) Register() byte {
	return WIFISSIDRegister
}

// RegisterVehicleVariant decodes register 0x29 (Steering wheel side RHD vs LHD and variant).
type RegisterVehicleVariant struct {
	Variant      byte
	SteeringRaw  byte
	SteeringSide string // "RHD", "LHD", "unknown"
	raw          []byte
}

func (r *RegisterVehicleVariant) Decode(m *PhevMessage) {
	if len(m.Data) < 2 {
		return
	}
	r.Variant = m.Data[0]
	r.SteeringRaw = m.Data[1]
	switch m.Data[1] {
	case 1:
		r.SteeringSide = "RHD"
	case 2:
		r.SteeringSide = "LHD"
	default:
		r.SteeringSide = "unknown"
	}
	r.raw = m.Data
}

func (r *RegisterVehicleVariant) Encode() *PhevMessage {
	data := []byte{r.Variant, r.SteeringRaw, 0x00}
	if data[1] == 0 {
		switch r.SteeringSide {
		case "RHD":
			data[1] = 1
		case "LHD":
			data[1] = 2
		}
	}
	return &PhevMessage{Register: r.Register(), Data: data}
}
func (r *RegisterVehicleVariant) Raw() string    { return hex.EncodeToString(r.raw) }
func (r *RegisterVehicleVariant) String() string { return fmt.Sprintf("Steering: %s", r.SteeringSide) }
func (r *RegisterVehicleVariant) Register() byte { return VehicleVariantRegister }

func NewPingRequestMessage(id byte) *PhevMessage {
	return NewMessage(CmdOutPingReq, id, false, []byte{0x0})
}

func NewPingResponseMessage(id byte) *PhevMessage {
	return NewMessage(CmdInPingResp, id, true, []byte{0x0})
}

func NewMessage(typ, register byte, ack bool, data []byte) *PhevMessage {
	msg := &PhevMessage{
		Type:     typ,
		Register: register,
		Data:     data,
	}
	if ack {
		msg.Ack = Ack
	}
	return msg
}

type RegisterLightStatus struct {
	ClimateTimerArmed   bool
	TheftAlarmRaw       byte
	TheftAlarm          string // "disarmed", "armed", "triggered"
	TheftAlarmTriggered bool
	Interior            bool
	Hazard              bool
	raw                 []byte
}

func (r *RegisterLightStatus) Encode() *PhevMessage {
	data := []byte{0x00, r.TheftAlarmRaw, 0x00, 0x02, 0x02}
	if r.ClimateTimerArmed {
		data[0] = 0x01
	}
	if data[1] == 0 {
		switch r.TheftAlarm {
		case "armed":
			data[1] = 1
		case "triggered":
			data[1] = 4
		default:
			data[1] = 7
		}
	}
	if r.Hazard {
		data[3] = 0x01
	}
	if r.Interior {
		data[4] = 0x01
	}
	return &PhevMessage{
		Register: r.Register(),
		Data:     data,
	}
}

func (r *RegisterLightStatus) Decode(m *PhevMessage) {
	if len(m.Data) != 5 {
		return
	}
	r.ClimateTimerArmed = m.Data[0] == 1
	r.TheftAlarmRaw = m.Data[1]
	switch m.Data[1] {
	case 1, 6:
		r.TheftAlarm = "armed"
		r.TheftAlarmTriggered = false
	case 4, 5:
		r.TheftAlarm = "triggered"
		r.TheftAlarmTriggered = true
	default:
		r.TheftAlarm = "disarmed"
		r.TheftAlarmTriggered = false
	}
	r.Hazard = (m.Data[3] == 1 || m.Data[3] == 3)
	r.Interior = (m.Data[4] == 1 || m.Data[4] == 3)
	r.raw = m.Data
}

func (r *RegisterLightStatus) Raw() string {
	return hex.EncodeToString(r.raw)
}

func (r *RegisterLightStatus) String() string {
	return fmt.Sprintf("Hazard: %t; Interior: %t; Alarm: %s.", r.Hazard, r.Interior, r.TheftAlarm)
}

func (r *RegisterLightStatus) Register() byte {
	return LightStatusRegister
}
