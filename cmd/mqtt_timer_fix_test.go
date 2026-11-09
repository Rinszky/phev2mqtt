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

	// Case 2: Scheduled climate timer triggers preheating at programmed time (e.g. 7:20-7:40).
	// RegisterPreACState reports PreACOn, while RegisterACOperStatus Operating may be false.
	stOn := protocol.PreACOn
	c.state = &stOn
	sRunning := c.mqttStates()
	if sRunning["/climate/heat"] != "on" || sRunning["/climate/mode"] != "heat" || sRunning["/climate/state"] != "heat" || sRunning["/climate/operating"] != "on" {
		t.Errorf("scheduled preheating active must report heat on and operating on: %v", sRunning)
	}

	// Case 3: Manual immediate AC operating.
	c.setOper(&protocol.RegisterACOperStatus{Operating: true})
	sManual := c.mqttStates()
	if sManual["/climate/heat"] != "on" || sManual["/climate/operating"] != "on" {
		t.Errorf("manual operating AC should be heat on and operating on: %v", sManual)
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
