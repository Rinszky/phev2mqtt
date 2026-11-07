package protocol

import (
	"encoding/hex"
	"testing"
)

func TestSettingsDecodeAndUpdate(t *testing.T) {
	frames := []string{
		"023a003b003c0000",
		"02e201e301640e00",
		"02e501a61ea71e00",
		"026b0e2c002d0000",
		"022e006f00300000",
		"0231007206730600",
		"02b4003506360e00",
		"02370e3806390000",
		"024106420603fe00",
		"02c41e850e861e00",
		"02473ec81e093f00",
		"02ca018bfe4c4300",
		"024d1e0e064f0600",
		"0210121106d20100",
		"02d301d401551e00",
		"02161ed701d80100",
		"0219061a23db0100",
		"02dc015d065e0600",
		"021f00600ee10100",
		"02e801e9016a3e00",
	}

	s := &Settings{}
	for _, f := range frames {
		b, err := hex.DecodeString(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FromRegister(b); err != nil {
			t.Fatalf("FromRegister(%s) error: %v", f, err)
		}
	}

	supported := s.SupportedSettings()
	if len(supported) < 30 {
		t.Fatalf("expected at least 30 supported settings in MY18 capture, got %d", len(supported))
	}

	// Verify 0x07 (Charging Lid Light Auto Cut-out): bytes 47 3e -> word 0x3e47 -> id=0x07, val=1 ("1 minute"), mask=0x1f
	vs07, ok := s.Get(0x07)
	if !ok || !vs07.Supported() {
		t.Fatalf("expected setting 0x07 to be supported")
	}
	if vs07.RawValue != 1 || vs07.ValueName() != "1 minute" {
		t.Fatalf("unexpected 0x07 value: raw=%d name=%q", vs07.RawValue, vs07.ValueName())
	}

	// Resolve by human-readable label and update in place.
	optIdx, err := vs07.ResolveOption("5 minutes")
	if err != nil || optIdx != 3 {
		t.Fatalf("ResolveOption failed: idx=%d err=%v", optIdx, err)
	}
	updatedFrame, ok := s.UpdateValue(0x07, optIdx)
	if !ok || len(updatedFrame) != 8 {
		t.Fatalf("UpdateValue(0x07, 3) failed")
	}

	vs07After, _ := s.GetBySlug("charging_lid_light_cutout")
	if vs07After.RawValue != 3 || vs07After.ValueName() != "5 minutes" {
		t.Fatalf("expected updated value '5 minutes', got %q", vs07After.ValueName())
	}
}
