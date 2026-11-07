package protocol

import (
	"encoding/hex"
	"gopkg.in/d4l3k/messagediff.v1"
	"testing"
)

func TestDecodeEncodeBytes(t *testing.T) {
	tests := []struct {
		in   string
		sk   *SecurityKey
		want *PhevMessage
	}{
		{
			in: "f60400060303",
			sk: &SecurityKey{keyMap: []byte{0x00, 0x00}},
			want: &PhevMessage{
				Type:          0xf6,
				Length:        0x6,
				Register:      0x6,
				Data:          []byte{0x3},
				Checksum:      0x3,
				Original:      []byte{0xf6, 0x4, 0x0, 0x6, 0x3, 0x3},
				OriginalXored: []byte{0xf6, 0x4, 0x0, 0x6, 0x3, 0x3},
			},
		}, {
			in: "502f3fff0f0f0a0d0f0d0d0f0f0f2f3e3f04",
			sk: &SecurityKey{keyMap: []byte{0x3f, 0x3f, 0x3f}},
			want: &PhevMessage{
				Type:          0x6f,
				Length:        0x12,
				Register:      0xc0,
				Data:          []byte{0x30, 0x30, 0x35, 0x32, 0x30, 0x32, 0x32, 0x30, 0x30, 0x30, 0x10, 0x1, 0x0},
				Checksum:      0x3b,
				Xor:           0x3f,
				Original:      []byte{0x6f, 0x10, 0x0, 0xc0, 0x30, 0x30, 0x35, 0x32, 0x30, 0x32, 0x32, 0x30, 0x30, 0x30, 0x10, 0x1, 0x0, 0x3b},
				OriginalXored: []byte{0x50, 0x2f, 0x3f, 0xff, 0x0f, 0x0f, 0x0a, 0x0d, 0x0f, 0x0d, 0x0d, 0x0f, 0x0f, 0x0f, 0x2f, 0x3e, 0x3f, 0x04},
			},
		}, {
			in: "caa2a5a7a5a5a5a5dd",
			sk: &SecurityKey{keyMap: []byte{0xa5, 0xa5}},
			want: &PhevMessage{
				Type:          0x6f,
				Length:        0x9,
				Register:      0x2,
				Data:          []byte{0x0, 0x0, 0x0, 0x0},
				Checksum:      0x78,
				Xor:           0xa5,
				Original:      []byte{0x6f, 0x7, 0x0, 0x2, 0x0, 0x0, 0x0, 0x0, 0x78},
				OriginalXored: []byte{0xca, 0xa2, 0xa5, 0xa7, 0xa5, 0xa5, 0xa5, 0xa5, 0xdd},
			},
		}, {
			in: "3cf4f13360d4",
			sk: &SecurityKey{keyMap: []byte{0xf0, 0xa5}},
			want: &PhevMessage{
				Type:          0xcc,
				Length:        0x6,
				Register:      0xc3,
				Data:          []byte{0x90},
				Checksum:      0x24,
				Xor:           0xf0,
				Ack:           Ack,
				Original:      []byte{0xcc, 0x4, 0x1, 0xc3, 0x90, 0x24},
				OriginalXored: []byte{0x3c, 0xf4, 0xf1, 0x33, 0x60, 0xd4},
			},
		}, {
			in: "4bf4f1c190a1",
			sk: &SecurityKey{keyMap: []byte{0xf0, 0xa5}},
			want: &PhevMessage{
				Type:          0xbb,
				Length:        0x6,
				Register:      0x31,
				Data:          []byte{0x60},
				Checksum:      0x51,
				Xor:           0xf0,
				Ack:           Ack,
				Original:      []byte{0xbb, 0x4, 0x1, 0x31, 0x60, 0x51},
				OriginalXored: []byte{0x4b, 0xf4, 0xf1, 0xc1, 0x90, 0xa1},
			},
		}, {
			in: "9ff6f0f3f1e59301",
			sk: &SecurityKey{keyMap: []byte{0xf0, 0xa5}},
			want: &PhevMessage{
				Type:          0x6f,
				Length:        0x8,
				Register:      0x3,
				Data:          []byte{0x1, 0x15, 0x63},
				Checksum:      0xf1,
				Xor:           0xf0,
				Original:      []byte{0x6f, 0x6, 0x0, 0x3, 0x1, 0x15, 0x63, 0xf1},
				OriginalXored: []byte{0x9f, 0xf6, 0xf0, 0xf3, 0xf1, 0xe5, 0x93, 0x01},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			data, err := hex.DecodeString(test.in)
			if err != nil {
				t.Fatal(err)
			}
			p := &PhevMessage{}
			if err := p.DecodeFromBytes(data, test.sk); err != nil {
				t.Fatalf("DecodeFromBytes() unexpected error: %v", err)
			}
			p.Reg = nil // Skip reg test for now.
			if diff, eq := messagediff.PrettyDiff(test.want, p); !eq {
				t.Fatalf("DecodeFromBytes() diff=%s", diff)
			}

			outData := test.want.EncodeToBytes(test.sk)
			gotData := hex.EncodeToString(outData)
			if gotData != test.in {
				t.Fatalf("EncodeToBytes: Unexpected. got=%s want=%s", gotData, test.in)
			}
		})
	}
}

func TestRegisterDecoders(t *testing.T) {
	// 1. Charge plug 0001 = connected (waiting for timer)
	plugMsg := &PhevMessage{Type: CmdInResp, Register: ChargePlugRegister, Data: []byte{0x00, 0x01}}
	var plug RegisterChargePlug
	plug.Decode(plugMsg)
	if !plug.Connected || !plug.WaitingForTimer || plug.PlugState != "connected_waiting" {
		t.Fatalf("unexpected ChargePlug 0001 decode: %+v", plug)
	}

	// 2. Battery level 0% and 3% valid, 0xff invalid
	for _, lvl := range []byte{0, 3, 100} {
		var bat RegisterBatteryLevel
		bat.Decode(&PhevMessage{Type: CmdInResp, Register: BatteryLevelRegister, Data: []byte{lvl, 0x00, 0x01, 0x00}})
		if !bat.Valid || bat.Level != int(lvl) || !bat.ParkingLights {
			t.Fatalf("expected valid battery level %d, got %+v", lvl, bat)
		}
	}
	var batInvalid RegisterBatteryLevel
	batInvalid.Decode(&PhevMessage{Type: CmdInResp, Register: BatteryLevelRegister, Data: []byte{0xff, 0x00, 0x00, 0x00}})
	if batInvalid.Valid {
		t.Fatalf("expected 0xff battery level to be marked invalid")
	}

	// 3. Pre-AC stop time 02b020 -> PreACOn, 16:32
	var preAC RegisterPreACState
	preAC.Decode(&PhevMessage{Type: CmdInResp, Register: PreACStateRegister, Data: []byte{0x02, 0xb0, 0x20}})
	if preAC.State != PreACOn || !preAC.HasTargetTime || preAC.TargetTimeString() != "16:32" {
		t.Fatalf("unexpected PreACState 02b020 decode: %+v (%s)", preAC, preAC.TargetTimeString())
	}

	// 4. Ignition & AC delay 0000000101 -> ignition off, delay 5m pending
	var oper RegisterACOperStatus
	oper.Decode(&PhevMessage{Type: CmdInResp, Register: ACOperStatusRegister, Data: []byte{0x00, 0x00, 0x00, 0x01, 0x01}})
	if oper.Ignition != "off" || oper.Delay != 5 || !oper.DelayPending {
		t.Fatalf("unexpected ACOperStatus decode: %+v", oper)
	}

	// 5. VIN 16-char compatibility + 17-char FullVIN
	vinBytes := make([]byte, 20)
	vinBytes[0] = 0x01
	copy(vinBytes[1:18], []byte("JMFXDGG2WJZ000480"))
	vinBytes[19] = 0x02
	var vinReg RegisterVIN
	vinReg.Decode(&PhevMessage{Type: CmdInResp, Register: VINRegister, Data: vinBytes})
	if vinReg.VIN != "JMFXDGG2WJZ00048" || vinReg.FullVIN != "JMFXDGG2WJZ000480" || vinReg.Registrations != 2 {
		t.Fatalf("unexpected VIN decode: %+v", vinReg)
	}

	// 6. LightStatus & Theft Alarm triggered
	var ls RegisterLightStatus
	ls.Decode(&PhevMessage{Type: CmdInResp, Register: LightStatusRegister, Data: []byte{0x01, 0x04, 0x00, 0x01, 0x02}})
	if !ls.ClimateTimerArmed || ls.TheftAlarm != "triggered" || !ls.TheftAlarmTriggered || !ls.Hazard || ls.Interior {
		t.Fatalf("unexpected LightStatus decode: %+v", ls)
	}
}
