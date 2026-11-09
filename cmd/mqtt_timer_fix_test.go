package cmd

import (
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

func TestClimateTimerArmedIsNotImmediateAC(t *testing.T) {
	// Case 1: Climate timer is armed in standby (waiting for scheduled time).
	// RegisterPreACState reports PreACStarting, RegisterACOperStatus has Operating=false.
	c := newClimateTracker()
	stStandby := protocol.PreACStarting
	c.state = &stStandby
	c.setOper(&protocol.RegisterACOperStatus{Operating: false, DelayPending: false})
	s := c.mqttStates()
	if s["/climate/heat"] != "off" || s["/climate/mode"] != "off" || s["/climate/state"] != "off" || s["/climate/operating"] != "off" {
		t.Errorf("armed timer in standby must report off: %v", s)
	}
	if s["/climate/remaining"] != "0" || s["/climate/completion_time"] != "none" {
		t.Errorf("armed timer in standby must report remaining 0: %v", s)
	}

	// Case 2: Scheduled climate timer triggers preheating at programmed time (e.g. 7:20-7:40).
	// RegisterPreACState reports PreACOn, while RegisterACOperStatus Operating may be false.
	stOn := protocol.PreACOn
	c.state = &stOn
	c.stopTime = "07:40"
	sRunning := c.mqttStates()
	if sRunning["/climate/heat"] != "on" || sRunning["/climate/mode"] != "heat" || sRunning["/climate/state"] != "heat" || sRunning["/climate/operating"] != "on" {
		t.Errorf("scheduled preheating active must report heat on and operating on: %v", sRunning)
	}
	if sRunning["/climate/stop_time"] != "07:40" {
		t.Errorf("expected stop_time 07:40: %v", sRunning)
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
	c := newClimateTracker()

	// 1. Off
	rem, comp := c.remainingAndCompletion(now)
	if rem != 0 || comp != "none" {
		t.Errorf("off should be 0 and none, got rem=%d comp=%s", rem, comp)
	}

	// 2. Running with stopTime 07:40
	stOn := protocol.PreACOn
	c.state = &stOn
	c.stopTime = "07:40"
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
