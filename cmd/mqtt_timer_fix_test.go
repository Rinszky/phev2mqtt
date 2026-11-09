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
	c.setState(&protocol.RegisterPreACState{
		State:         protocol.PreACOn,
		HasTargetTime: true,
		TargetHour:    7,
		TargetMinute:  20,
	})
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
	c.setOper(&protocol.RegisterACOperStatus{Operating: true})
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
	})
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
