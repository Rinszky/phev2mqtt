package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	// Read registers for schedules and master timer states.
	ChargeTimersRegister      = 0x04
	ClimateTimersRegister     = 0x05
	ChargeTimerOverrideReg    = 0x17
	ChargeTimerStateRegister  = 0x1b

	// Write registers for schedules and master timer toggles.
	SetClimateTimerMasterReg  = 0x03 // 0x02 = enable, 0x01 = disable
	SetChargeTimerMasterReg   = 0x17 // 0x02 = enable, 0x01 / 0x11 = disable/cancel
	SetChargeTimersMY18Reg    = 0x19 // 21 bytes
	SetClimateTimersMY18Reg   = 0x1a // 16 bytes
)

// Day bitmask constants (bit 0 = Sunday .. bit 6 = Saturday).
const (
	DaySun byte = 1 << 0
	DayMon byte = 1 << 1
	DayTue byte = 1 << 2
	DayWed byte = 1 << 3
	DayThu byte = 1 << 4
	DayFri byte = 1 << 5
	DaySat byte = 1 << 6

	DaysNone     byte = 0x00
	DaysWeekdays byte = DayMon | DayTue | DayWed | DayThu | DayFri
	DaysWeekend  byte = DaySun | DaySat
	DaysEveryday byte = 0x7f
)

var dayNamesShort = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

var dayTokenMap = map[string]byte{
	// English
	"sun": DaySun, "sunday": DaySun, "su": DaySun,
	"mon": DayMon, "monday": DayMon, "mo": DayMon,
	"tue": DayTue, "tues": DayTue, "tuesday": DayTue, "tu": DayTue,
	"wed": DayWed, "wednesday": DayWed, "we": DayWed,
	"thu": DayThu, "thur": DayThu, "thurs": DayThu, "thursday": DayThu, "th": DayThu,
	"fri": DayFri, "friday": DayFri, "fr": DayFri,
	"sat": DaySat, "saturday": DaySat, "sa": DaySat,
	// Hungarian
	"v": DaySun, "vas": DaySun, "vasarnap": DaySun, "vasárnap": DaySun,
	"h": DayMon, "het": DayMon, "hetfo": DayMon, "hétfő": DayMon,
	"k": DayTue, "kedd": DayTue,
	"sze": DayWed, "szerda": DayWed,
	"cs": DayThu, "csut": DayThu, "csutortok": DayThu, "csütörtök": DayThu,
	"p": DayFri, "pen": DayFri, "pentek": DayFri, "péntek": DayFri,
	"szo": DaySat, "szombat": DaySat,
	// ISO / numeric (0 or 7 = Sunday, 1 = Monday .. 6 = Saturday)
	"0": DaySun, "7": DaySun,
	"1": DayMon, "2": DayTue, "3": DayWed, "4": DayThu, "5": DayFri, "6": DaySat,
}

// DaysMaskToString formats a 7-bit day mask into a concise human-readable string.
func DaysMaskToString(mask byte) string {
	mask &= 0x7f
	switch mask {
	case DaysNone:
		return "None"
	case DaysEveryday:
		return "Everyday"
	}
	// Output in Monday..Sunday calendar order for European/ISO readability while preserving round-trip parsing.
	order := [7]int{1, 2, 3, 4, 5, 6, 0}
	parts := make([]string, 0, 7)
	for _, idx := range order {
		if mask&(1<<idx) != 0 {
			parts = append(parts, dayNamesShort[idx])
		}
	}
	return strings.Join(parts, ",")
}

// ParseDaysMask parses English, Hungarian, or preset day specifications into a 7-bit mask.
func ParseDaysMask(input string) (byte, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	switch s {
	case "", "none", "off", "unset", "reset", "ki", "nincs", "-":
		return DaysNone, nil
	case "everyday", "all", "daily", "mindennap", "minden nap":
		return DaysEveryday, nil
	case "weekdays", "weekday", "workdays", "mon-fri", "h-p", "hetkoznap", "hétköznap":
		return DaysWeekdays, nil
	case "weekend", "weekends", "sat-sun", "szo-v", "hetvege", "hétvége":
		return DaysWeekend, nil
	}

	norm := strings.NewReplacer(";", ",", "|", ",", "/", ",", " ", ",").Replace(s)
	tokens := strings.Split(norm, ",")
	var mask byte
	matched := 0
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		bit, ok := dayTokenMap[tok]
		if !ok {
			return 0, fmt.Errorf("unknown day token %q", tok)
		}
		mask |= bit
		matched++
	}
	if matched == 0 {
		return DaysNone, nil
	}
	return mask & 0x7f, nil
}

// ParseTimerHHMM parses "HH:MM" (or "--:--"/"unset" to clear) and rounds minutes to the nearest 10-minute step.
func ParseTimerHHMM(input string) (hour int, minute int, configured bool, err error) {
	s := strings.TrimSpace(strings.ToLower(input))
	switch s {
	case "", "--:--", "-", "unset", "reset", "clear", "none", "off", "ki":
		return 0, 0, false, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false, fmt.Errorf("expected HH:MM format, got %q", input)
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false, fmt.Errorf("invalid time %q (expected 00:00..23:59)", input)
	}
	// Round to nearest 10-minute increment required by the vehicle ECU (00, 10, 20, 30, 40, 50).
	rounded := ((m + 5) / 10) * 10
	if rounded >= 60 {
		rounded = 0
		h = (h + 1) % 24
	}
	return h, rounded, true, nil
}

// ClimateTimerSlot represents one of the 5 MY18+ climate pre-conditioning schedule slots (3 bytes).
type ClimateTimerSlot struct {
	Index      int    `json:"index"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled"`
	Hour       int    `json:"hour"`
	Minute     int    `json:"minute"`
	Duration   int    `json:"duration"` // 10, 20, or 30 minutes
	DaysMask   byte   `json:"days_mask"`
}

type climateTimerJSON struct {
	Index      int    `json:"index"`
	Configured *bool  `json:"configured,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Time       string `json:"time,omitempty"`
	Duration   *int   `json:"duration,omitempty"`
	Days       string `json:"days,omitempty"`
}

// DefaultClimateTimerSlot returns an empty/unset climate timer slot.
func DefaultClimateTimerSlot(index int) ClimateTimerSlot {
	return ClimateTimerSlot{
		Index:      index,
		Configured: false,
		Enabled:    false,
		Hour:       7,
		Minute:     0,
		Duration:   10,
		DaysMask:   DaysNone,
	}
}

func (c ClimateTimerSlot) TimeString() string {
	if !c.Configured {
		return "--:--"
	}
	return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute)
}

func (c ClimateTimerSlot) DaysString() string {
	if !c.Configured {
		return "None"
	}
	return DaysMaskToString(c.DaysMask)
}

func (c ClimateTimerSlot) Summary() string {
	if !c.Configured {
		return fmt.Sprintf("T%d: unset", c.Index)
	}
	state := "OFF"
	if c.Enabled {
		state = "ON"
	}
	return fmt.Sprintf("T%d: %s %s (%dm, %s)", c.Index, state, c.TimeString(), c.Duration, c.DaysString())
}

func (c ClimateTimerSlot) ToJSON() string {
	cfg := c.Configured
	en := c.Configured && c.Enabled
	dur := c.Duration
	if dur != 10 && dur != 20 && dur != 30 {
		dur = 10
	}
	payload := climateTimerJSON{
		Index:      c.Index,
		Configured: &cfg,
		Enabled:    &en,
		Time:       c.TimeString(),
		Duration:   &dur,
		Days:       c.DaysString(),
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func (c *ClimateTimerSlot) ApplyJSON(raw string) error {
	var in climateTimerJSON
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return err
	}
	if in.Configured != nil && !*in.Configured {
		*c = DefaultClimateTimerSlot(c.Index)
		return nil
	}
	if in.Time != "" {
		h, m, cfg, err := ParseTimerHHMM(in.Time)
		if err != nil {
			return err
		}
		if !cfg {
			*c = DefaultClimateTimerSlot(c.Index)
			return nil
		}
		c.Hour = h
		c.Minute = m
		c.Configured = true
	}
	if in.Duration != nil {
		switch *in.Duration {
		case 10, 20, 30:
			c.Duration = *in.Duration
		default:
			return fmt.Errorf("invalid climate timer duration %d (must be 10, 20, or 30)", *in.Duration)
		}
		c.Configured = true
	}
	if in.Days != "" {
		mask, err := ParseDaysMask(in.Days)
		if err != nil {
			return err
		}
		c.DaysMask = mask
		if mask != DaysNone {
			c.Configured = true
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
		if *in.Enabled {
			c.Configured = true
		}
	}
	c.EnsureValidDefaults()
	return nil
}

// EnsureValidDefaults populates sensible defaults when a previously unset slot is configured/enabled.
func (c *ClimateTimerSlot) EnsureValidDefaults() {
	if !c.Configured {
		return
	}
	if c.Duration != 10 && c.Duration != 20 && c.Duration != 30 {
		c.Duration = 10
	}
	if c.Hour < 0 || c.Hour > 23 {
		c.Hour = 7
	}
	if c.Minute < 0 || c.Minute > 50 {
		c.Minute = 0
	}
	c.Minute = (c.Minute / 10) * 10
	if c.DaysMask == DaysNone {
		c.DaysMask = DaysEveryday
	}
}

// DecodeClimateTimerSlot decodes a 3-byte MY18 climate timer entry.
func DecodeClimateTimerSlot(index int, b []byte) ClimateTimerSlot {
	slot := DefaultClimateTimerSlot(index)
	if len(b) < 3 {
		return slot
	}
	durIdx := b[0] & 0x03
	days := ((b[0] >> 2) | ((b[1] & 0x01) << 6)) & 0x7f
	minIdx := (b[1] >> 1) & 0x07
	hour := ((b[1] >> 4) | ((b[2] & 0x01) << 4)) & 0x1f
	enableState := (b[2] >> 1) & 0x03

	if enableState == 3 || enableState == 0 || hour > 23 || minIdx > 5 {
		return slot
	}
	slot.Configured = true
	slot.Enabled = enableState == 1
	slot.Hour = int(hour)
	slot.Minute = int(minIdx) * 10
	switch durIdx {
	case 0:
		slot.Duration = 10
	case 1:
		slot.Duration = 20
	case 2:
		slot.Duration = 30
	default:
		slot.Duration = 30
	}
	slot.DaysMask = days
	return slot
}

// Encode encodes a ClimateTimerSlot into its 3-byte wire representation.
func (c ClimateTimerSlot) Encode() [3]byte {
	if !c.Configured {
		return [3]byte{0x00, 0xfe, 0x07}
	}
	var durIdx byte
	switch {
	case c.Duration <= 10:
		durIdx = 0
	case c.Duration <= 20:
		durIdx = 1
	default:
		durIdx = 2
	}
	days := c.DaysMask & 0x7f
	if days == 0 {
		days = DaysEveryday
	}
	minIdx := byte((c.Minute / 10) & 0x07)
	if minIdx > 5 {
		minIdx = 5
	}
	hr := byte(c.Hour & 0x1f)
	if hr > 23 {
		hr = 0
	}
	en := byte(2)
	if c.Enabled {
		en = 1
	}
	return [3]byte{
		(durIdx & 0x03) | ((days & 0x3f) << 2),
		((days >> 6) & 0x01) | (minIdx << 1) | ((hr & 0x0f) << 4),
		((hr >> 4) & 0x01) | (en << 1),
	}
}

// ClimateSchedule holds the full 5-slot MY18+ climate schedule and master enable/mode.
type ClimateSchedule struct {
	MasterEnabled bool
	Mode          string // "cool", "heat", "windscreen"
	Timers        [5]ClimateTimerSlot
}

// NewDefaultClimateSchedule returns an initialized empty 5-slot climate schedule.
func NewDefaultClimateSchedule() ClimateSchedule {
	s := ClimateSchedule{Mode: "heat"}
	for i := 0; i < 5; i++ {
		s.Timers[i] = DefaultClimateTimerSlot(i + 1)
	}
	return s
}

// DecodeRead0x05 decodes the 16-byte Read 0x05 register ([masterEnable, 5 x 3B timers]).
func (s *ClimateSchedule) DecodeRead0x05(data []byte) error {
	if len(data) != 16 {
		return fmt.Errorf("climate timer 0x05 wrong length got=%d want=16", len(data))
	}
	s.MasterEnabled = data[0] == 0x02
	for i := 0; i < 5; i++ {
		s.Timers[i] = DecodeClimateTimerSlot(i+1, data[1+i*3:4+i*3])
	}
	if s.Mode == "" {
		s.Mode = "heat"
	}
	return nil
}

// EncodeRead0x05 encodes the 16-byte Read 0x05 payload ([masterEnable, 5 x 3B timers]).
func (s *ClimateSchedule) EncodeRead0x05() []byte {
	out := make([]byte, 16)
	if s.MasterEnabled || s.AnyEnabled() {
		out[0] = 0x02
	} else {
		out[0] = 0x01
	}
	for i := 0; i < 5; i++ {
		b := s.Timers[i].Encode()
		copy(out[1+i*3:4+i*3], b[:])
	}
	return out
}

// DecodeWrite0x1a decodes the 16-byte Write 0x1a payload ([5 x 3B timers, modeByte]).
func (s *ClimateSchedule) DecodeWrite0x1a(data []byte) error {
	if len(data) != 16 {
		return fmt.Errorf("climate timer write 0x1a wrong length got=%d want=16", len(data))
	}
	for i := 0; i < 5; i++ {
		s.Timers[i] = DecodeClimateTimerSlot(i+1, data[i*3:3+i*3])
	}
	switch data[15] {
	case 1:
		s.Mode = "cool"
	case 2:
		s.Mode = "heat"
	case 3:
		s.Mode = "windscreen"
	default:
		s.Mode = "heat"
	}
	s.MasterEnabled = s.AnyEnabled()
	return nil
}

// EncodeWrite0x1a encodes the 16-byte Write 0x1a payload ([5 x 3B timers, modeByte]).
func (s *ClimateSchedule) EncodeWrite0x1a() []byte {
	out := make([]byte, 16)
	for i := 0; i < 5; i++ {
		b := s.Timers[i].Encode()
		copy(out[i*3:3+i*3], b[:])
	}
	switch s.Mode {
	case "cool":
		out[15] = 0x01
	case "windscreen":
		out[15] = 0x03
	default:
		out[15] = 0x02 // default heat
	}
	return out
}

func (s *ClimateSchedule) AnyEnabled() bool {
	for i := 0; i < 5; i++ {
		if s.Timers[i].Configured && s.Timers[i].Enabled {
			return true
		}
	}
	return false
}

// ChargeTimerSlot represents one of the 5 MY18+ charge schedule slots (4 bytes).
type ChargeTimerSlot struct {
	Index       int  `json:"index"`
	Configured  bool `json:"configured"`
	Enabled     bool `json:"enabled"`
	StartHour   int  `json:"start_hour"`
	StartMinute int  `json:"start_minute"`
	EndHour     int  `json:"end_hour"`
	EndMinute   int  `json:"end_minute"`
	FullCharge  bool `json:"full_charge"`
	DaysMask    byte `json:"days_mask"`
}

type chargeTimerJSON struct {
	Index      int    `json:"index"`
	Configured *bool  `json:"configured,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Start      string `json:"start,omitempty"`
	End        string `json:"end,omitempty"`
	FullCharge *bool  `json:"full_charge,omitempty"`
	Days       string `json:"days,omitempty"`
}

// DefaultChargeTimerSlot returns an empty/unset charge timer slot.
func DefaultChargeTimerSlot(index int) ChargeTimerSlot {
	return ChargeTimerSlot{
		Index:       index,
		Configured:  false,
		Enabled:     false,
		StartHour:   23,
		StartMinute: 0,
		EndHour:     6,
		EndMinute:   0,
		FullCharge:  true,
		DaysMask:    DaysNone,
	}
}

func (c ChargeTimerSlot) StartString() string {
	if !c.Configured {
		return "--:--"
	}
	return fmt.Sprintf("%02d:%02d", c.StartHour, c.StartMinute)
}

func (c ChargeTimerSlot) EndString() string {
	if !c.Configured {
		return "--:--"
	}
	return fmt.Sprintf("%02d:%02d", c.EndHour, c.EndMinute)
}

func (c ChargeTimerSlot) DaysString() string {
	if !c.Configured {
		return "None"
	}
	return DaysMaskToString(c.DaysMask)
}

func (c ChargeTimerSlot) Summary() string {
	if !c.Configured {
		return fmt.Sprintf("T%d: unset", c.Index)
	}
	state := "OFF"
	if c.Enabled {
		state = "ON"
	}
	fc := ""
	if c.FullCharge {
		fc = " [Full]"
	}
	return fmt.Sprintf("T%d: %s %s-%s%s (%s)", c.Index, state, c.StartString(), c.EndString(), fc, c.DaysString())
}

func (c ChargeTimerSlot) ToJSON() string {
	cfg := c.Configured
	en := c.Configured && c.Enabled
	fc := c.FullCharge
	payload := chargeTimerJSON{
		Index:      c.Index,
		Configured: &cfg,
		Enabled:    &en,
		Start:      c.StartString(),
		End:        c.EndString(),
		FullCharge: &fc,
		Days:       c.DaysString(),
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func (c *ChargeTimerSlot) ApplyJSON(raw string) error {
	var in chargeTimerJSON
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return err
	}
	if in.Configured != nil && !*in.Configured {
		*c = DefaultChargeTimerSlot(c.Index)
		return nil
	}
	if in.Start != "" {
		h, m, cfg, err := ParseTimerHHMM(in.Start)
		if err != nil {
			return err
		}
		if !cfg {
			*c = DefaultChargeTimerSlot(c.Index)
			return nil
		}
		c.StartHour = h
		c.StartMinute = m
		c.Configured = true
	}
	if in.End != "" {
		h, m, cfg, err := ParseTimerHHMM(in.End)
		if err != nil {
			return err
		}
		if !cfg {
			*c = DefaultChargeTimerSlot(c.Index)
			return nil
		}
		c.EndHour = h
		c.EndMinute = m
		c.Configured = true
	}
	if in.FullCharge != nil {
		c.FullCharge = *in.FullCharge
		c.Configured = true
	}
	if in.Days != "" {
		mask, err := ParseDaysMask(in.Days)
		if err != nil {
			return err
		}
		c.DaysMask = mask
		if mask != DaysNone {
			c.Configured = true
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
		if *in.Enabled {
			c.Configured = true
		}
	}
	c.EnsureValidDefaults()
	return nil
}

// EnsureValidDefaults populates sensible defaults when an unset charge timer slot is configured/enabled.
func (c *ChargeTimerSlot) EnsureValidDefaults() {
	if !c.Configured {
		return
	}
	if c.StartHour < 0 || c.StartHour > 23 {
		c.StartHour = 23
	}
	if c.EndHour < 0 || c.EndHour > 23 {
		c.EndHour = 6
	}
	c.StartMinute = (c.StartMinute / 10) * 10
	if c.StartMinute < 0 || c.StartMinute > 50 {
		c.StartMinute = 0
	}
	c.EndMinute = (c.EndMinute / 10) * 10
	if c.EndMinute < 0 || c.EndMinute > 50 {
		c.EndMinute = 0
	}
	if c.DaysMask == DaysNone {
		c.DaysMask = DaysEveryday
	}
}

// DecodeChargeTimerSlot decodes a 4-byte MY18 charge timer entry.
func DecodeChargeTimerSlot(index int, b []byte) ChargeTimerSlot {
	slot := DefaultChargeTimerSlot(index)
	if len(b) < 4 {
		return slot
	}
	fc := (b[0] & 0x01) != 0
	days := (b[0] >> 1) & 0x7f
	sMin := b[1] & 0x07
	sHr := (b[1] >> 3) & 0x1f
	eMin := b[2] & 0x07
	eHr := (b[2] >> 3) & 0x1f
	enState := b[3] & 0x03

	if enState == 3 || enState == 0 || sHr > 23 || sMin > 5 || eHr > 23 || eMin > 5 {
		return slot
	}
	slot.Configured = true
	slot.Enabled = enState == 1
	slot.FullCharge = fc
	slot.DaysMask = days
	slot.StartHour = int(sHr)
	slot.StartMinute = int(sMin) * 10
	slot.EndHour = int(eHr)
	slot.EndMinute = int(eMin) * 10
	return slot
}

// Encode encodes a ChargeTimerSlot into its 4-byte wire representation.
func (c ChargeTimerSlot) Encode() [4]byte {
	if !c.Configured {
		return [4]byte{0x00, 0xff, 0xff, 0x03}
	}
	var fc byte
	if c.FullCharge {
		fc = 1
	}
	days := c.DaysMask & 0x7f
	if days == 0 {
		days = DaysEveryday
	}
	sMin := byte((c.StartMinute / 10) & 0x07)
	if sMin > 5 {
		sMin = 5
	}
	eMin := byte((c.EndMinute / 10) & 0x07)
	if eMin > 5 {
		eMin = 5
	}
	sHr := byte(c.StartHour & 0x1f)
	if sHr > 23 {
		sHr = 0
	}
	eHr := byte(c.EndHour & 0x1f)
	if eHr > 23 {
		eHr = 0
	}
	en := byte(0x02)
	if c.Enabled {
		en = 0x01
	}
	return [4]byte{
		fc | (days << 1),
		sMin | (sHr << 3),
		eMin | (eHr << 3),
		en,
	}
}

// ChargeSchedule holds the full 5-slot MY18+ charge schedule and master enable state.
type ChargeSchedule struct {
	MasterEnabled bool
	Timers        [5]ChargeTimerSlot
}

// NewDefaultChargeSchedule returns an initialized empty 5-slot charge schedule.
func NewDefaultChargeSchedule() ChargeSchedule {
	var s ChargeSchedule
	for i := 0; i < 5; i++ {
		s.Timers[i] = DefaultChargeTimerSlot(i + 1)
	}
	return s
}

// DecodeRead0x04 decodes the 20-byte Read 0x04 payload (5 x 4B timers).
func (s *ChargeSchedule) DecodeRead0x04(data []byte) error {
	if len(data) != 20 {
		return fmt.Errorf("charge timer 0x04 wrong length got=%d want=20", len(data))
	}
	for i := 0; i < 5; i++ {
		s.Timers[i] = DecodeChargeTimerSlot(i+1, data[i*4:4+i*4])
	}
	return nil
}

// EncodeRead0x04 encodes the 20-byte Read 0x04 payload (5 x 4B timers).
func (s *ChargeSchedule) EncodeRead0x04() []byte {
	out := make([]byte, 20)
	for i := 0; i < 5; i++ {
		b := s.Timers[i].Encode()
		copy(out[i*4:4+i*4], b[:])
	}
	return out
}

// DecodeWrite0x19 decodes the 21-byte Write 0x19 payload (5 x 4B timers + 1B mode).
func (s *ChargeSchedule) DecodeWrite0x19(data []byte) error {
	if len(data) != 21 {
		return fmt.Errorf("charge timer write 0x19 wrong length got=%d want=21", len(data))
	}
	for i := 0; i < 5; i++ {
		s.Timers[i] = DecodeChargeTimerSlot(i+1, data[i*4:4+i*4])
	}
	s.MasterEnabled = s.AnyEnabled() && data[20] >= 2
	return nil
}

// EncodeWrite0x19 encodes the 21-byte Write 0x19 payload (5 x 4B timers + 1B mode).
func (s *ChargeSchedule) EncodeWrite0x19() []byte {
	out := make([]byte, 21)
	anyConfigured := false
	for i := 0; i < 5; i++ {
		if s.Timers[i].Configured {
			anyConfigured = true
		}
		b := s.Timers[i].Encode()
		copy(out[i*4:4+i*4], b[:])
	}
	if anyConfigured {
		out[20] = 0x02
	} else {
		out[20] = 0x00
	}
	return out
}

func (s *ChargeSchedule) AnyEnabled() bool {
	for i := 0; i < 5; i++ {
		if s.Timers[i].Configured && s.Timers[i].Enabled {
			return true
		}
	}
	return false
}
