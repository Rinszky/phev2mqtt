package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestClimateTimerRealMY18Vectors(t *testing.T) {
	// Vector 1 from protocol/README.md:
	// t1 12:00 cool for 10 mins repeat sun, t2 07:50 cool for 20 mins repeat sunMonTue:
	// set 0x1a: 04c0021d7a0200fe0700fe0700fe0701
	raw1a, err := hex.DecodeString("04c0021d7a0200fe0700fe0700fe0701")
	if err != nil {
		t.Fatal(err)
	}
	var sched ClimateSchedule
	if err := sched.DecodeWrite0x1a(raw1a); err != nil {
		t.Fatalf("DecodeWrite0x1a failed: %v", err)
	}
	if sched.Mode != "cool" {
		t.Fatalf("expected mode=cool, got %q", sched.Mode)
	}
	t1 := sched.Timers[0]
	if !t1.Configured || !t1.Enabled || t1.Hour != 12 || t1.Minute != 0 || t1.Duration != 10 || t1.DaysMask != DaySun {
		t.Fatalf("unexpected T1: %+v", t1)
	}
	t2 := sched.Timers[1]
	wantDays2 := DaySun | DayMon | DayTue
	if !t2.Configured || !t2.Enabled || t2.Hour != 7 || t2.Minute != 50 || t2.Duration != 20 || t2.DaysMask != wantDays2 {
		t.Fatalf("unexpected T2: %+v", t2)
	}
	if sched.Timers[2].Configured || sched.Timers[3].Configured || sched.Timers[4].Configured {
		t.Fatalf("expected T3..T5 to be unset")
	}

	// Verify exact byte-for-byte round-trip of Write 0x1a.
	reencoded1a := sched.EncodeWrite0x1a()
	if !bytes.Equal(reencoded1a, raw1a) {
		t.Fatalf("EncodeWrite0x1a mismatch:\n got: %x\nwant: %x", reencoded1a, raw1a)
	}

	// Verify Read 0x05 layout ([master=0x02, 5x3B]).
	read05 := sched.EncodeRead0x05()
	if read05[0] != 0x02 || len(read05) != 16 {
		t.Fatalf("unexpected EncodeRead0x05 header/len: %x", read05)
	}
	var sched2 ClimateSchedule
	if err := sched2.DecodeRead0x05(read05); err != nil {
		t.Fatalf("DecodeRead0x05 failed: %v", err)
	}
	if !sched2.MasterEnabled || sched2.Timers[0].TimeString() != "12:00" || sched2.Timers[1].TimeString() != "07:50" {
		t.Fatalf("DecodeRead0x05 mismatch: %+v", sched2)
	}

	// Verify disabled vector: 04c0041d7a0400fe0700fe0700fe0701
	disabled1a, _ := hex.DecodeString("04c0041d7a0400fe0700fe0700fe0701")
	var schedDis ClimateSchedule
	if err := schedDis.DecodeWrite0x1a(disabled1a); err != nil {
		t.Fatal(err)
	}
	if !schedDis.Timers[0].Configured || schedDis.Timers[0].Enabled || schedDis.AnyEnabled() {
		t.Fatalf("expected disabled configured slots, got %+v", schedDis)
	}
	if got := hex.EncodeToString(schedDis.EncodeWrite0x1a()); got != "04c0041d7a0400fe0700fe0700fe0701" {
		t.Fatalf("disabled round-trip mismatch: got %s", got)
	}
}

func TestChargeTimerRealMY18Vectors(t *testing.T) {
	// Real MY18 capture from protocol/README.md:
	// 7d38b00183bd00017c70380200ffff0300ffff0302
	raw19, err := hex.DecodeString("7d38b00183bd00017c70380200ffff0300ffff0302")
	if err != nil {
		t.Fatal(err)
	}
	var sched ChargeSchedule
	if err := sched.DecodeWrite0x19(raw19); err != nil {
		t.Fatalf("DecodeWrite0x19 failed: %v", err)
	}
	if !sched.MasterEnabled {
		t.Fatalf("expected MasterEnabled=true")
	}

	// T1: 7d 38 b0 01 -> fc=1, days=0x3e (Weekdays), start=07:00, end=22:00, enabled=true
	t1 := sched.Timers[0]
	if !t1.Configured || !t1.Enabled || !t1.FullCharge || t1.DaysMask != DaysWeekdays || t1.StartString() != "07:00" || t1.EndString() != "22:00" {
		t.Fatalf("unexpected charge T1: %+v", t1)
	}
	// T3: 7c 70 38 02 -> fc=0, days=0x3e, start=14:00, end=07:00, enabled=false
	t3 := sched.Timers[2]
	if !t3.Configured || t3.Enabled || t3.FullCharge || t3.StartString() != "14:00" || t3.EndString() != "07:00" {
		t.Fatalf("unexpected charge T3: %+v", t3)
	}
	if sched.Timers[3].Configured || sched.Timers[4].Configured {
		t.Fatalf("expected T4 and T5 unset")
	}

	reencoded19 := sched.EncodeWrite0x19()
	if !bytes.Equal(reencoded19, raw19) {
		t.Fatalf("EncodeWrite0x19 mismatch:\n got: %x\nwant: %x", reencoded19, raw19)
	}

	read04 := sched.EncodeRead0x04()
	if !bytes.Equal(read04, raw19[:20]) {
		t.Fatalf("EncodeRead0x04 mismatch:\n got: %x\nwant: %x", read04, raw19[:20])
	}
}

func TestParseDaysAndTimeHelpers(t *testing.T) {
	mask, err := ParseDaysMask("H, K, Sze, Cs, P")
	if err != nil || mask != DaysWeekdays {
		t.Fatalf("Hungarian weekdays parse failed: mask=0x%02x err=%v", mask, err)
	}
	mask2, err := ParseDaysMask("hétvége")
	if err != nil || mask2 != DaysWeekend {
		t.Fatalf("Hungarian weekend parse failed: mask=0x%02x err=%v", mask2, err)
	}
	h, mn, cfg, err := ParseTimerHHMM("06:56")
	if err != nil || !cfg || h != 7 || mn != 0 {
		t.Fatalf("expected 06:56 to round to 07:00, got %02d:%02d cfg=%t err=%v", h, mn, cfg, err)
	}
	_, _, cfgClear, err := ParseTimerHHMM("--:--")
	if err != nil || cfgClear {
		t.Fatalf("expected --:-- to clear slot, got cfg=%t err=%v", cfgClear, err)
	}
}
