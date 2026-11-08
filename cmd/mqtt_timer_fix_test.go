package cmd

import (
	"testing"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

func TestClimateTimerArmedIsNotImmediateAC(t *testing.T) {
	c := newClimateTracker()
	st := protocol.PreACOn
	c.state = &st
	c.setOper(&protocol.RegisterACOperStatus{Operating: false, DelayPending: false})
	s := c.mqttStates()
	if s["/climate/heat"] != "off" || s["/climate/mode"] != "off" || s["/climate/state"] != "off" {
		t.Errorf("armed timer must not look like running AC: %v", s)
	}
	c.setOper(&protocol.RegisterACOperStatus{Operating: true})
	if c.mqttStates()["/climate/heat"] != "on" {
		t.Errorf("operating AC should be heat on")
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
