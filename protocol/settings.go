package protocol

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// Write registers for vehicle customization settings (ETACS / AC).
	SetCommitSettingsRegister = 0x0e
	SetUpdateSettingRegister  = 0x0f
)

// SettingDefinition describes a known Mitsubishi PHEV customization setting.
type SettingDefinition struct {
	ID       byte
	Slug     string
	Name     string
	Category string
	Options  map[byte]string
}

// VehicleSetting represents the live decoded state and capability mask of one setting on the car.
type VehicleSetting struct {
	ID            byte
	Slug          string
	Name          string
	Category      string
	RawValue      byte
	SupportedMask byte
}

// Supported reports whether this vehicle supports/equips this setting.
func (v *VehicleSetting) Supported() bool {
	return v != nil && v.ID >= 1 && v.ID <= 60 && v.SupportedMask != 0 && v.RawValue < 7
}

// OptionLabel returns the human-readable label for a raw option index (0..6).
func (v *VehicleSetting) OptionLabel(opt byte) string {
	if def, ok := SettingDefinitions[v.ID]; ok {
		if lbl, ok2 := def.Options[opt]; ok2 {
			return lbl
		}
	}
	return fmt.Sprintf("Option %d", opt)
}

// ValueName returns the current value's human-readable option string.
func (v *VehicleSetting) ValueName() string {
	if v == nil || v.RawValue >= 7 {
		return "unsupported"
	}
	return v.OptionLabel(v.RawValue)
}

// SupportedOptionIndices returns the sorted option indices (0..6) supported by this vehicle.
func (v *VehicleSetting) SupportedOptionIndices() []byte {
	if v == nil {
		return nil
	}
	var out []byte
	for i := byte(0); i < 7; i++ {
		if v.SupportedMask&(1<<i) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// SupportedOptions returns the human-readable option strings supported by this vehicle.
func (v *VehicleSetting) SupportedOptions() []string {
	indices := v.SupportedOptionIndices()
	seen := make(map[string]int, len(indices))
	for _, idx := range indices {
		seen[v.OptionLabel(idx)]++
	}
	out := make([]string, 0, len(indices))
	for _, idx := range indices {
		lbl := v.OptionLabel(idx)
		if seen[lbl] > 1 {
			lbl = fmt.Sprintf("%s (%d)", lbl, idx)
		}
		out = append(out, lbl)
	}
	return out
}

// ResolveOption converts an option label or numeric index string into a valid supported raw value (0..6).
func (v *VehicleSetting) ResolveOption(input string) (byte, error) {
	if v == nil || v.SupportedMask == 0 {
		return 0, fmt.Errorf("setting is not supported on this vehicle")
	}
	trimmed := strings.TrimSpace(input)
	indices := v.SupportedOptionIndices()
	opts := v.SupportedOptions()

	// Exact or case-insensitive match against supported option labels first.
	for i, lbl := range opts {
		if strings.EqualFold(lbl, trimmed) {
			return indices[i], nil
		}
	}
	// Match raw base label if unique among supported options.
	for _, idx := range indices {
		if strings.EqualFold(v.OptionLabel(idx), trimmed) {
			return idx, nil
		}
	}
	// Numeric index fallback ("0".."6").
	if n, err := strconv.Atoi(trimmed); err == nil && n >= 0 && n <= 6 {
		b := byte(n)
		if v.SupportedMask&(1<<b) == 0 {
			return 0, fmt.Errorf("option %d is not supported for setting 0x%02x (mask 0x%02x)", n, v.ID, v.SupportedMask)
		}
		return b, nil
	}
	return 0, fmt.Errorf("invalid option %q for %s (valid: %s)", input, v.Name, strings.Join(opts, ", "))
}

// SettingDefinitions maps Mitsubishi setting IDs (0x01..0x3c) to their official names and options.
var SettingDefinitions = map[byte]SettingDefinition{
	0x01: {
		ID: 0x01, Slug: "wipers_key_position", Name: "Wipers Operation in Key Position", Category: "Wipers",
		Options: map[byte]string{0: "Ignition On or ACC", 1: "Ignition On"},
	},
	0x02: {
		ID: 0x02, Slug: "lane_change_3flash", Name: "Lane-change 3-Flash Turn Signal", Category: "Turn Signal",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x03: {
		ID: 0x03, Slug: "turn_signal_answerback", Name: "Turn Signal Lights Answerback", Category: "Keyless Entry",
		Options: map[byte]string{
			0: "Lock: Once / Unlock: Twice",
			1: "Lock: Once / Unlock: Off",
			2: "Lock: Off / Unlock: Twice",
			3: "Lock: Twice / Unlock: Once",
			4: "Lock: Off / Unlock: Once",
			5: "Lock: Twice / Unlock: Off",
			6: "Off",
		},
	},
	0x04: {
		ID: 0x04, Slug: "front_wiper_intermittent", Name: "Windshield Wipers Intermittent Operation", Category: "Wipers",
		Options: map[byte]string{
			0: "Fixed (4 seconds)",
			1: "Adjustable",
			2: "Adjustable & Speed Sensitive",
			3: "Adjustable & Rain Sensitive",
		},
	},
	0x05: {
		ID: 0x05, Slug: "wipers_linked_to_washer", Name: "Wipers Linked to Washer", Category: "Wipers",
		Options: map[byte]string{0: "Off", 1: "On", 2: "On with Delayed Finishing Wipe"},
	},
	0x06: {
		ID: 0x06, Slug: "rear_wiper_interval", Name: "Rear Wiper Intermittent Interval", Category: "Wipers",
		Options: map[byte]string{0: "0 seconds", 1: "4 seconds", 2: "8 seconds", 3: "16 seconds"},
	},
	0x07: {
		ID: 0x07, Slug: "charging_lid_light_cutout", Name: "Charging Lid Light Auto Cut-out", Category: "Lights",
		Options: map[byte]string{0: "Off", 1: "1 minute", 2: "3 minutes", 3: "5 minutes", 4: "10 minutes"},
	},
	0x08: {
		ID: 0x08, Slug: "auto_fold_mirrors", Name: "Auto Fold Mirrors", Category: "Mirrors",
		Options: map[byte]string{
			0: "Off",
			1: "Open by Vehicle Speed",
			2: "Open/Close by IG On/Off & Driver Door",
			3: "Open/Close by Keyless Lock/Unlock",
		},
	},
	0x09: {
		ID: 0x09, Slug: "auto_light_sensitivity", Name: "Sensitivity for Auto Light", Category: "Lights",
		Options: map[byte]string{0: "Early", 1: "Somewhat Early", 2: "Normal", 3: "Somewhat Late", 4: "Late"},
	},
	0x0a: {
		ID: 0x0a, Slug: "auto_light_linked_wiper", Name: "Auto Light Linked to Wiper", Category: "Lights",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x0b: {
		ID: 0x0b, Slug: "dome_light_delay", Name: "Dome Light Duration After Door Closed", Category: "Lights",
		Options: map[byte]string{
			0: "0 seconds",
			1: "7.5 seconds",
			2: "15 seconds",
			3: "30 seconds",
			4: "1 minute",
			5: "2 minutes",
			6: "3 minutes",
		},
	},
	0x0c: {
		ID: 0x0c, Slug: "headlight_auto_cutout", Name: "Headlight Auto-cutout Function", Category: "Lights",
		Options: map[byte]string{
			0: "Off",
			1: "Tail/Head Lamp: Cutout Always",
			2: "On",
			3: "On",
			4: "Tail: Cutout Once / Head: Cutout Always",
			5: "On",
		},
	},
	0x0d: {
		ID: 0x0d, Slug: "interior_light_auto_cutout", Name: "Interior Light Auto-cutout Time", Category: "Lights",
		Options: map[byte]string{0: "Off", 1: "3 minutes", 2: "30 minutes", 3: "60 minutes"},
	},
	0x0e: {
		ID: 0x0e, Slug: "lane_change_activation_time", Name: "Activation Time for Lane-change Signals", Category: "Turn Signal",
		Options: map[byte]string{0: "Short", 1: "Long"},
	},
	0x0f: {
		ID: 0x0f, Slug: "comfort_washer", Name: "Comfort Washer", Category: "Wipers",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x10: {
		ID: 0x10, Slug: "automatic_door_unlock", Name: "Automatic Door Unlock", Category: "Door Locks",
		Options: map[byte]string{
			0: "Off",
			1: "When Shifted to Park",
			2: "When Shifted to Park (Windows Unlocked)",
			3: "When Ignition Turned to Lock",
			4: "When Ignition Turned to Lock (Windows Unlocked)",
		},
	},
	0x11: {
		ID: 0x11, Slug: "selective_door_unlock", Name: "Unlock Operation", Category: "Door Locks",
		Options: map[byte]string{0: "All Doors", 1: "Only Driver Door"},
	},
	0x13: {
		ID: 0x13, Slug: "deadlock_button_pushes", Name: "Lock Button Pushes for Dead Lock", Category: "Door Locks",
		Options: map[byte]string{0: "2 times", 1: "1 time"},
	},
	0x14: {
		ID: 0x14, Slug: "horn_answerback_lock", Name: "Horn Answerback at Keyless Lock", Category: "Keyless Entry",
		Options: map[byte]string{
			0: "Off",
			1: "One Button Push",
			2: "One Button Push (Daytime)",
			3: "Two Button Pushes",
			4: "Two Button Pushes (Daytime)",
		},
	},
	0x15: {
		ID: 0x15, Slug: "kos_answerback_beep", Name: "Keyless Answerback Beep", Category: "Keyless Operation",
		Options: map[byte]string{
			0: "All Systems Off",
			1: "Sound at Keyless Operation",
			2: "Sound at Keyless Entry",
			3: "All Systems On",
		},
	},
	0x16: {
		ID: 0x16, Slug: "auto_relock_time", Name: "Automatic Relocking After Remote Unlock", Category: "Keyless Entry",
		Options: map[byte]string{0: "30 seconds", 1: "1 minute", 2: "2 minutes", 3: "3 minutes"},
	},
	0x18: {
		ID: 0x18, Slug: "power_window_keyoff_timer", Name: "Power Window Timer After IG Off", Category: "Power Windows",
		Options: map[byte]string{0: "0 seconds", 1: "30 seconds", 2: "3 minutes", 3: "10 minutes"},
	},
	0x1a: {
		ID: 0x1a, Slug: "multi_mode_keyless", Name: "Multi Mode Keyless Entry System", Category: "Keyless Entry",
		Options: map[byte]string{
			0: "Off",
			1: "Window & Mirror: Open & Close",
			2: "Power Window: Open & Close",
			3: "Window: Close / Mirror: Open & Close",
			4: "Door Mirror: Open & Close",
			5: "Power Window: Close",
		},
	},
	0x1c: {
		ID: 0x1c, Slug: "horn_answerback_duration", Name: "Duration of Horn Answerback", Category: "Keyless Entry",
		Options: map[byte]string{0: "Short", 1: "Long"},
	},
	0x1d: {
		ID: 0x1d, Slug: "rear_wiper_in_reverse", Name: "Rear Wiper Activated in Reverse", Category: "Wipers",
		Options: map[byte]string{0: "When Rear Wiper is On", 1: "When Front or Rear Wiper is On"},
	},
	0x20: {
		ID: 0x20, Slug: "remote_unlock_inactivation", Name: "Remote Unlock Inactivation After Locking", Category: "Keyless Entry",
		Options: map[byte]string{0: "Off", 1: "3 seconds", 2: "5 seconds"},
	},
	0x21: {
		ID: 0x21, Slug: "horn_remote_start", Name: "Horn Answerback at Remote Start", Category: "Keyless Entry",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x22: {
		ID: 0x22, Slug: "window_open_after_ig_off", Name: "Window Open Operation After IG Off", Category: "Power Windows",
		Options: map[byte]string{0: "On", 1: "Off"},
	},
	0x23: {
		ID: 0x23, Slug: "window_lock_switch_scope", Name: "Power Window Lock-switch Scope", Category: "Power Windows",
		Options: map[byte]string{0: "All Doors", 1: "Only Driver Door"},
	},
	0x24: {
		ID: 0x24, Slug: "acc_power_auto_cutout", Name: "Auto Cut of ACC Power", Category: "Other",
		Options: map[byte]string{0: "Off", 1: "30 minutes", 2: "60 minutes"},
	},
	0x2a: {
		ID: 0x2a, Slug: "headlights_on_exit", Name: "Headlights ON When Exiting Vehicle", Category: "Lights",
		Options: map[byte]string{0: "Off", 1: "15 seconds", 2: "30 seconds", 3: "1 minute", 4: "3 minutes"},
	},
	0x2b: {
		ID: 0x2b, Slug: "exterior_lights_remote_unlock", Name: "Exterior Lights ON with Remote Unlock", Category: "Lights",
		Options: map[byte]string{0: "Off", 1: "Parking lights", 2: "Head lights"},
	},
	0x32: {
		ID: 0x32, Slug: "ac_recirculation_control", Name: "Recirculation Control", Category: "Air Conditioner",
		Options: map[byte]string{0: "Manual", 1: "Automatic"},
	},
	0x33: {
		ID: 0x33, Slug: "ac_switch_control", Name: "A/C Switch Control", Category: "Air Conditioner",
		Options: map[byte]string{0: "Manual", 1: "Automatic"},
	},
	0x35: {
		ID: 0x35, Slug: "ac_eco_mode", Name: "A/C Eco Mode", Category: "Air Conditioner",
		Options: map[byte]string{0: "Eco", 1: "Comfort"},
	},
	0x36: {
		ID: 0x36, Slug: "foot_defroster_airflow_ratio", Name: "Foot / Defroster Airflow Ratio", Category: "Air Conditioner",
		Options: map[byte]string{0: "Normal", 1: "More Airflow to Foot", 2: "More Airflow to Defroster"},
	},
	0x37: {
		ID: 0x37, Slug: "face_foot_airflow_ratio", Name: "Face / Foot Airflow Ratio", Category: "Air Conditioner",
		Options: map[byte]string{0: "Normal", 1: "More Airflow to Face", 2: "More Airflow to Foot"},
	},
	0x38: {
		ID: 0x38, Slug: "auto_rear_defrost_at_start", Name: "Automatic Rear Defrost at Start", Category: "Air Conditioner",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x39: {
		ID: 0x39, Slug: "start_ac_at_door_unlock", Name: "Start A/C or Heater at Door Unlock", Category: "Air Conditioner",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x3a: {
		ID: 0x3a, Slug: "foot_mode_defroster_bleed", Name: "Slight Defroster Airflow in Foot Mode", Category: "Air Conditioner",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
	0x3c: {
		ID: 0x3c, Slug: "fast_idle_heater_boost", Name: "Fast Idle for Higher Heater Performance", Category: "Air Conditioner",
		Options: map[byte]string{0: "Off", 1: "On"},
	},
}

// DecodeSettingWord decodes one 16-bit LE setting descriptor from a 0x16 register packet.
func DecodeSettingWord(word uint16) *VehicleSetting {
	id := byte(word & 0x3f)
	if id == 0 {
		return nil
	}
	val := byte((word >> 6) & 0x07)
	mask := byte((word >> 9) & 0x7f)

	def, ok := SettingDefinitions[id]
	slug := def.Slug
	name := def.Name
	cat := def.Category
	if !ok {
		slug = fmt.Sprintf("setting_%02x", id)
		name = fmt.Sprintf("Vehicle Setting 0x%02x", id)
		cat = "Other"
	}
	return &VehicleSetting{
		ID:            id,
		Slug:          slug,
		Name:          name,
		Category:      cat,
		RawValue:      val,
		SupportedMask: mask,
	}
}

// EncodeSettingWord packs a VehicleSetting back into a 16-bit LE word for 0x16 packets.
func EncodeSettingWord(id, rawVal, supportedMask byte) uint16 {
	return uint16(id&0x3f) | (uint16(rawVal&0x07) << 6) | (uint16(supportedMask&0x7f) << 9)
}

// EncodeUpdateSetting builds the 2-byte payload for write register 0x0f ([setting_id, option_index]).
func EncodeUpdateSetting(id, optionIndex byte) []byte {
	return []byte{id, optionIndex}
}

// EncodeFactoryResetSettings builds the 2-byte payload for write register 0x0f ([0xfe, 0x00]).
func EncodeFactoryResetSettings() []byte {
	return []byte{0xfe, 0x00}
}

// EncodeCommitSettings builds the 1-byte payload for write register 0x0e ([0x00]).
func EncodeCommitSettings() []byte {
	return []byte{0x00}
}

// DecodeSettingsFrame extracts up to 3 VehicleSetting entries from an 8-byte 0x16 payload.
func DecodeSettingsFrame(reg []byte) ([]*VehicleSetting, error) {
	switch {
	case len(reg) != 8:
		return nil, fmt.Errorf("register wrong length got=%d want=8", len(reg))
	case reg[0] != 0x2:
		return nil, fmt.Errorf("register must start with 0x2, is 0x%x", reg[0])
	case reg[7] != 0x0:
		return nil, fmt.Errorf("register must end with 0x0, is 0x%x", reg[7])
	}
	out := make([]*VehicleSetting, 0, 3)
	for i := 0; i < 3; i++ {
		w := binary.LittleEndian.Uint16(reg[1+i*2 : 3+i*2])
		if vs := DecodeSettingWord(w); vs != nil {
			out = append(out, vs)
		}
	}
	return out, nil
}

func frameKeyIDs(reg []byte) [3]byte {
	return [3]byte{
		reg[1] & 0x3f,
		reg[3] & 0x3f,
		reg[5] & 0x3f,
	}
}

// Vehicle settings are sent to the client in register 0x16.
// The client stages updated settings via register 0x0f and commits them via register 0x0e.
type Settings struct {
	mu       sync.RWMutex
	settings []uint64
	items    map[byte]*VehicleSetting
}

// FromRegister extracts and decodes settings from an 8-byte 0x16 register frame.
func (s *Settings) FromRegister(reg []byte) error {
	decoded, err := DecodeSettingsFrame(reg)
	if err != nil {
		return err
	}
	value := binary.LittleEndian.Uint64(reg)
	newKey := frameKeyIDs(reg)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		s.items = make(map[byte]*VehicleSetting)
	}
	for _, vs := range decoded {
		cp := *vs
		s.items[vs.ID] = &cp
	}

	replaced := false
	for idx, existing := range s.settings {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], existing)
		if frameKeyIDs(buf[:]) == newKey {
			s.settings[idx] = value
			replaced = true
			break
		}
	}
	if !replaced {
		s.settings = append(s.settings, value)
	}
	return nil
}

// UpdateValue updates a single setting's value in memory (used e.g. by the emulator on 0x0f/0x0e)
// and returns the updated 8-byte 0x16 frame containing that setting, if found.
func (s *Settings) UpdateValue(id, val byte) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	vs, ok := s.items[id]
	if !ok || !vs.Supported() || (vs.SupportedMask&(1<<val)) == 0 {
		return nil, false
	}
	vs.RawValue = val

	for idx, raw := range s.settings {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], raw)
		matched := false
		for slot := 0; slot < 3; slot++ {
			w := binary.LittleEndian.Uint16(buf[1+slot*2 : 3+slot*2])
			if byte(w&0x3f) == id {
				newW := EncodeSettingWord(id, val, vs.SupportedMask)
				binary.LittleEndian.PutUint16(buf[1+slot*2:3+slot*2], newW)
				matched = true
			}
		}
		if matched {
			s.settings[idx] = binary.LittleEndian.Uint64(buf[:])
			out := make([]byte, 8)
			copy(out, buf[:])
			return out, true
		}
	}
	return nil, false
}

// Get returns a copy of the VehicleSetting for the given ID (0x01..0x3c).
func (s *Settings) Get(id byte) (*VehicleSetting, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.items == nil {
		return nil, false
	}
	vs, ok := s.items[id]
	if !ok {
		return nil, false
	}
	cp := *vs
	return &cp, true
}

// GetBySlug looks up a VehicleSetting by its slug or hex ID ("0x08" / "8").
func (s *Settings) GetBySlug(slugOrID string) (*VehicleSetting, bool) {
	key := strings.TrimSpace(strings.ToLower(slugOrID))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, vs := range s.items {
		if strings.ToLower(vs.Slug) == key {
			cp := *vs
			return &cp, true
		}
	}
	cleanHex := strings.TrimPrefix(key, "0x")
	if n, err := strconv.ParseUint(cleanHex, 16, 8); err == nil {
		if vs, ok := s.items[byte(n)]; ok {
			cp := *vs
			return &cp, true
		}
	}
	if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= 60 {
		if vs, ok := s.items[byte(n)]; ok {
			cp := *vs
			return &cp, true
		}
	}
	return nil, false
}

// SupportedSettings returns all settings supported by the vehicle, sorted by ID.
func (s *Settings) SupportedSettings() []*VehicleSetting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*VehicleSetting, 0, len(s.items))
	for _, vs := range s.items {
		if vs.Supported() {
			cp := *vs
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// AllSettings returns all decoded settings (supported and unsupported), sorted by ID.
func (s *Settings) AllSettings() []*VehicleSetting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*VehicleSetting, 0, len(s.items))
	for _, vs := range s.items {
		cp := *vs
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Settings) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = []uint64{}
	s.items = make(map[byte]*VehicleSetting)
}

func (s *Settings) NewSender() *SettingsSender {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := append([]uint64(nil), s.settings...)
	return &SettingsSender{settings: cp}
}

func (s *Settings) Dump() string {
	s.mu.RLock()
	rawList := append([]uint64(nil), s.settings...)
	s.mu.RUnlock()

	ret := []string{}
	for _, v := range rawList {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], v)
		frames, _ := DecodeSettingsFrame(buf[:])
		parts := []string{}
		for _, f := range frames {
			if f.Supported() {
				parts = append(parts, fmt.Sprintf("0x%02x %s=%s", f.ID, f.Slug, f.ValueName()))
			}
		}
		if len(parts) > 0 {
			ret = append(ret, fmt.Sprintf("%016x (%s)", v, strings.Join(parts, "; ")))
		} else {
			ret = append(ret, fmt.Sprintf("%016x", v))
		}
	}
	return strings.Join(ret, "\n")
}

type SettingsSender struct {
	C        chan *PhevMessage
	settings []uint64
}

func (s *SettingsSender) Start() {
	s.C = make(chan *PhevMessage, len(s.settings))
	go func() {
		for i := 0; i < len(s.settings); i++ {
			buf := make([]byte, 8)
			binary.LittleEndian.PutUint64(buf, s.settings[i])
			s.C <- NewMessage(CmdInResp, 0x16, false, buf)
		}
		close(s.C)
	}()
}
