package cmd

import (
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

func TestClimateTimerArmedIsNotImmediateAC(t *testing.T) {
	sched := protocol.NewDefaultClimateSchedule()
	sched.Timers[0].Enabled = true
	sched.Timers[0].Hour = 7
	sched.Timers[0].Minute = 20
	sched.Timers[0].Duration = 20

	// Case 1: Climate timer is armed in standby (waiting for scheduled time).
	// RegisterPreACState reports PreACStarting with start time 07:20, RegisterACOperStatus has Operating=false.
	c := newClimateTracker(&sched)
	c.setState(&protocol.RegisterPreACState{
		State:         protocol.PreACStarting,
		HasTargetTime: true,
		TargetHour:    7,
		TargetMinute:  20,
	})
	c.setOper(&protocol.RegisterACOperStatus{Operating: false, DelayPending: false})
	s := c.mqttStates()
	if s["/climate/heat"] != "off" || s["/climate/mode"] != "off" || s["/climate/state"] != "off" || s["/climate/operating"] != "off" {
		t.Errorf("armed timer in standby must report off: %v", s)
	}
	if s["/climate/remaining"] != "0" || s["/climate/completion_time"] != "none" || s["/climate/stop_time"] != "--:--" {
		t.Errorf("armed timer in standby must report remaining 0: %v", s)
	}

	// Case 2: Scheduled climate timer triggers preheating at programmed time (7:20).
	// RegisterPreACState reports PreACOn with start time 07:20. Duration is 20 min -> stop time MUST be 07:40!
	testTime := time.Date(2026, 11, 9, 7, 20, 0, 0, time.Local)
	c.setState(&protocol.RegisterPreACState{
		State:         protocol.PreACOn,
		HasTargetTime: true,
		TargetHour:    7,
		TargetMinute:  20,
	}, testTime)
	sRunning := c.mqttStates()
	if sRunning["/climate/heat"] != "on" || sRunning["/climate/mode"] != "heat" || sRunning["/climate/state"] != "heat" || sRunning["/climate/operating"] != "on" {
		t.Errorf("scheduled preheating active must report heat on and operating on: %v", sRunning)
	}
	if sRunning["/climate/start_time"] != "07:20" {
		t.Errorf("expected start_time 07:20, got: %s", sRunning["/climate/start_time"])
	}
	if sRunning["/climate/stop_time"] != "07:40" {
		t.Errorf("expected stop_time 07:40, got: %s", sRunning["/climate/stop_time"])
	}

	// Case 3: Manual immediate AC operating.
	c.setOper(&protocol.RegisterACOperStatus{Operating: true}, testTime)
	sManual := c.mqttStates()
	if sManual["/climate/heat"] != "on" || sManual["/climate/operating"] != "on" {
		t.Errorf("manual operating AC should be heat on and operating on: %v", sManual)
	}
}

func TestClimateRemainingAndCompletion(t *testing.T) {
	now := time.Date(2026, 11, 9, 7, 25, 10, 0, time.UTC)
	sched := protocol.NewDefaultClimateSchedule()
	sched.Timers[0].Enabled = true
	sched.Timers[0].Hour = 7
	sched.Timers[0].Minute = 20
	sched.Timers[0].Duration = 20

	c := newClimateTracker(&sched)

	// 1. Off
	rem, comp := c.remainingAndCompletion(now)
	if rem != 0 || comp != "none" {
		t.Errorf("off should be 0 and none, got rem=%d comp=%s", rem, comp)
	}

	// 2. Running with start time 07:20 and 20 min timer -> stop time 07:40, at 07:25 remaining = 15 min!
	c.setState(&protocol.RegisterPreACState{
		State:         protocol.PreACOn,
		HasTargetTime: true,
		TargetHour:    7,
		TargetMinute:  20,
	}, now)
	rem, comp = c.remainingAndCompletion(now)
	if rem != 15 || comp != "2026-11-09T07:40:00Z" {
		t.Errorf("running expected rem=15 comp=2026-11-09T07:40:00Z, got rem=%d comp=%s", rem, comp)
	}

	// 3. Delayed start pending: delay 5 min, duration 20 min -> total 25 min
	c2 := newClimateTracker()
	c2.selectedDelay = 5
	c2.selectedDuration = 20
	c2.delayPending = true
	c2.delayStartTime = now
	rem, comp = c2.remainingAndCompletion(now)
	if rem != 25 || comp != "2026-11-09T07:50:00Z" {
		t.Errorf("delayed expected rem=25 comp=2026-11-09T07:50:00Z, got rem=%d comp=%s", rem, comp)
	}

	// 4. Manual AC (no target time from 0x10): duration 20 min, countdown decreases over time!
	c3 := newClimateTracker()
	c3.selectedDuration = 20
	c3.operating = true
	c3.operatingStartTime = now
	rem0, _ := c3.remainingAndCompletion(now)
	if rem0 != 20 {
		t.Errorf("manual at T0 expected 20, got %d", rem0)
	}
	rem5, _ := c3.remainingAndCompletion(now.Add(5 * time.Minute))
	if rem5 != 15 {
		t.Errorf("manual at T+5m expected 15, got %d", rem5)
	}
	rem19, _ := c3.remainingAndCompletion(now.Add(19 * time.Minute))
	if rem19 != 1 {
		t.Errorf("manual at T+19m expected 1, got %d", rem19)
	}
	rem20, _ := c3.remainingAndCompletion(now.Add(20 * time.Minute))
	if rem20 != 0 {
		t.Errorf("manual at T+20m expected 0, got %d", rem20)
	}
}

func TestChargeCompletionTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC)
	if got := chargeCompletionTime(true, 90, now); got != "2026-01-01T11:30:00Z" {
		t.Errorf("got %s", got)
	}
	if chargeCompletionTime(false, 90, now) != "none" || chargeCompletionTime(true, 0, now) != "none" {
		t.Errorf("expected none")
	}
}

func TestScaleBatteryLevel(t *testing.T) {
	tests := []struct {
		raw    int
		maxSoc int
		want   int
	}{
		{raw: 0, maxSoc: 94, want: 0},
		{raw: 47, maxSoc: 94, want: 50},
		{raw: 88, maxSoc: 94, want: 94},
		{raw: 89, maxSoc: 94, want: 95},
		{raw: 90, maxSoc: 94, want: 96},
		{raw: 93, maxSoc: 94, want: 99},
		{raw: 94, maxSoc: 94, want: 100},
		{raw: 95, maxSoc: 94, want: 100},
		// Disabled scaling (maxSoc = 100)
		{raw: 94, maxSoc: 100, want: 94},
		{raw: 100, maxSoc: 100, want: 100},
	}
	for _, tc := range tests {
		got := scaleBatteryLevel(tc.raw, tc.maxSoc)
		if got != tc.want {
			t.Errorf("scaleBatteryLevel(%d, %d) = %d; want %d", tc.raw, tc.maxSoc, got, tc.want)
		}
	}
}

func TestProcessBatteryLevelGlitchFilter(t *testing.T) {
	m := newMqttClient()
	m.batteryMaxSoc = 94

	// 1. Initial valid reading 94% -> scaled 100%
	reg94 := &protocol.RegisterBatteryLevel{Level: 94, Valid: true}
	scaled, raw, isGlitch := m.processBatteryLevel(reg94)
	if isGlitch || scaled != "100" || raw != "94" {
		t.Fatalf("initial reading expected (100, 94, false), got (%s, %s, %v)", scaled, raw, isGlitch)
	}
	// Simulate MQTT cache update done by publish()
	m.mqttData["/battery/level"] = scaled
	m.mqttData["/battery/raw_level"] = raw

	// 2. Sudden momentary drop to 5% (simulating ECU wake glitch)
	reg5 := &protocol.RegisterBatteryLevel{Level: 5, Valid: true}
	scaled, raw, isGlitch = m.processBatteryLevel(reg5)
	if !isGlitch {
		t.Fatalf("first 5%% drop should be detected as glitch, got isGlitch=false")
	}
	if scaled != "100" || raw != "94" {
		t.Fatalf("glitch should return cached valid levels (100, 94), got (%s, %s)", scaled, raw)
	}

	// 3. Second momentary packet of 5% (still within glitch count < 3)
	scaled, raw, isGlitch = m.processBatteryLevel(reg5)
	if !isGlitch || scaled != "100" || raw != "94" {
		t.Fatalf("second 5%% drop should still be filtered as glitch, got (%s, %s, %v)", scaled, raw, isGlitch)
	}

	// 4. Car returns to normal 94% on next packet -> glitch filter resets and accepts 94%
	scaled, raw, isGlitch = m.processBatteryLevel(reg94)
	if isGlitch || scaled != "100" || raw != "94" {
		t.Fatalf("recovery packet should be accepted immediately, got (%s, %s, %v)", scaled, raw, isGlitch)
	}
	if m.batteryGlitchCount != 0 {
		t.Fatalf("glitch count should be reset to 0, got %d", m.batteryGlitchCount)
	}

	// 5. Normal gradual discharge (e.g. 94 -> 93 -> 92 -> ... -> 90) must pass immediately
	reg93 := &protocol.RegisterBatteryLevel{Level: 93, Valid: true}
	scaled, raw, isGlitch = m.processBatteryLevel(reg93)
	if isGlitch || scaled != "99" || raw != "93" {
		t.Fatalf("gradual discharge to 93 must be accepted immediately, got (%s, %s, %v)", scaled, raw, isGlitch)
	}
	m.mqttData["/battery/level"] = scaled
	m.mqttData["/battery/raw_level"] = raw

	// 6. Real sustained drop to 5%: 3 consecutive packets should confirm and accept 5%
	m.processBatteryLevel(reg5)                         // count 1
	m.processBatteryLevel(reg5)                         // count 2
	scaled, raw, isGlitch = m.processBatteryLevel(reg5) // count 3 -> accepted!
	if isGlitch {
		t.Fatalf("sustained 3rd reading of 5%% must be accepted as real")
	}
	if raw != "5" {
		t.Fatalf("expected raw 5, got %s", raw)
	}
}

func TestManualPreheatingTenMinGlitchFixed(t *testing.T) {
	// Car has a scheduled morning timer for 20 minutes (7:00).
	sched := protocol.NewDefaultClimateSchedule()
	sched.Timers[0].Enabled = true
	sched.Timers[0].Hour = 7
	sched.Timers[0].Minute = 0
	sched.Timers[0].Duration = 20

	c := newClimateTracker(&sched)
	c.selectedDuration = 10
	c.operatingStartTime = time.Now()
	c.hasStartTime = false

	// When 0x10 arrives indicating PreACOn (with lingering bytes in target time):
	c.setState(&protocol.RegisterPreACState{
		State:         protocol.PreACOn,
		HasTargetTime: true,
		TargetHour:    7,
		TargetMinute:  20,
	})

	if dur := c.activeDuration(); dur != 10 {
		t.Fatalf("activeDuration must stay 10 mins during active manual session, got %d", dur)
	}

	st := c.mqttStates()
	if st["/climate/duration"] != "10" {
		t.Errorf("duration must be 10, got %s", st["/climate/duration"])
	}
	if rem := st["/climate/remaining"]; rem != "10" {
		t.Errorf("remaining minutes must start at 10, got %s", rem)
	}
}

