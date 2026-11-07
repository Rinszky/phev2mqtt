package emulator

import (
	"encoding/hex"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
)

func (s *Connection) manage() {
	l := s.AddListener()
	pingCount := 0
	defer func() {
		l.Stop()
		s.RemoveListener(l)
	}()
	for {
		select {
		case <-s.done:
			return
		case msg, ok := <-l.C:
			if !ok {
				return
			}
			switch msg.Type {
			case protocol.CmdOutPingReq:
				pingCount++
				s.SendMsg(protocol.NewPingResponseMessage(msg.Register))
				if s.key.State == protocol.SecurityEmpty && pingCount == 10 {
					s.state = conSecInit
					s.rekey()
				}
			case protocol.CmdOutMy14StartResp, protocol.CmdOutMy18StartResp, protocol.CmdOutMy24StartResp:
				switch msg.Type {
				case protocol.CmdOutMy14StartResp:
					s.startReqCmd = protocol.CmdInMy14StartReq
				case protocol.CmdOutMy24StartResp:
					s.startReqCmd = protocol.CmdInMy24StartReq
				default:
					s.startReqCmd = protocol.CmdInMy18StartReq
				}
				if len(msg.Original) > 2 && msg.Original[2] == 0x0 {
					s.rekey()
					break
				}
				if s.key.State == protocol.SecurityKeyProposed {
					s.key.AcceptProposal()
					s.state = conEstablished
					s.registerIndex = 0
					s.sendNextRegister()
				}
			case protocol.CmdOutSend:
				if msg.Ack == protocol.Ack {
					s.sendNextRegister()
				}
				if msg.Ack == protocol.Request {
					s.handleSetRegister(msg)
				}
			}
		}
	}
}

func (s *Connection) emitRegister(reg byte, data []byte) {
	cp := append([]byte(nil), data...)
	s.car.UpdateCachedRegister(reg, cp)
	s.SendMsg(protocol.NewMessage(protocol.CmdInResp, reg, false, cp))
}

func (s *Connection) handleSetRegister(msg *protocol.PhevMessage) {
	// Ack the write request first.
	s.SendMsg(protocol.NewMessage(protocol.CmdInResp, msg.Register, true, []byte{0x0}))

	switch msg.Register {
	case protocol.SetACModeRegisterMY14: // 0x02 (15B)
		if len(msg.Data) >= 7 {
			s.emitRegister(protocol.ACModeRegister, []byte{msg.Data[6]})
		}

	case protocol.SetClimateTimerMasterReg: // 0x03
		if len(msg.Data) >= 1 {
			cur := s.car.GetCachedRegisterBytes(protocol.ClimateTimersRegister)
			if len(cur) != 16 {
				sched := protocol.NewDefaultClimateSchedule()
				cur = sched.EncodeRead0x05()
			}
			if msg.Data[0] == 0x02 {
				cur[0] = 0x02
			} else {
				cur[0] = 0x01
			}
			s.emitRegister(protocol.ClimateTimersRegister, cur)

			ls := s.car.GetCachedRegisterBytes(protocol.LightStatusRegister)
			if len(ls) == 5 {
				if cur[0] == 0x02 {
					ls[0] = 0x01
				} else {
					ls[0] = 0x00
				}
				s.emitRegister(protocol.LightStatusRegister, ls)
			}
		}

	case protocol.SetACEnabledRegisterMY14: // 0x04
		if len(msg.Data) >= 1 && msg.Data[0] == 0x02 {
			s.emitRegister(protocol.ACOperStatusRegister, []byte{0x00, 0x01, 0x00, 0x00, 0x00})
			s.emitRegister(protocol.PreACStateRegister, []byte{byte(protocol.PreACOn), 0x00, 0x00})
		} else {
			s.emitRegister(protocol.ACOperStatusRegister, []byte{0x00, 0x00, 0x00, 0x00, 0x00})
			s.emitRegister(protocol.PreACStateRegister, []byte{byte(protocol.PreACOff), 0x00, 0x00})
		}

	case protocol.SetSyncTimeRegister: // 0x05 (7B or 8B)
		if len(msg.Data) >= 7 {
			s.emitRegister(protocol.TimeRegister, msg.Data[:7])
			time.Sleep(20 * time.Millisecond)
			bat := s.car.GetCachedRegisterBytes(protocol.BatteryLevelRegister)
			if len(bat) != 4 {
				bat = []byte{0x50, 0x00, 0x00, 0x00}
			}
			s.SendMsg(protocol.NewMessage(protocol.CmdInResp, protocol.BatteryLevelRegister, false, bat))
		}

	case protocol.SetRequestUpdateRegister: // 0x06
		if len(msg.Data) >= 1 && msg.Data[0] == 0x03 {
			s.settingsSender = nil
			s.registerIndex = 0
			s.sendNextRegister()
		}

	case protocol.SetAckTheftAlarmRegister: // 0x08
		s.emitRegister(protocol.TheftAlarmEventRegister, make([]byte, 7))

	case protocol.SetHeadlightsRegister: // 0x0a
		if len(msg.Data) >= 1 {
			doors := s.car.GetCachedRegisterBytes(protocol.DoorStatusRegister)
			if len(doors) != 10 {
				doors = []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0}
			}
			if msg.Data[0] == 0x01 {
				doors[9] = 0x01
			} else {
				doors[9] = 0x00
			}
			s.emitRegister(protocol.DoorStatusRegister, doors)
		}

	case protocol.SetParkingLightsRegister: // 0x0b
		if len(msg.Data) >= 1 {
			bat := s.car.GetCachedRegisterBytes(protocol.BatteryLevelRegister)
			if len(bat) != 4 {
				bat = []byte{0x50, 0x00, 0x00, 0x00}
			}
			if msg.Data[0] == 0x01 {
				bat[2] = 0x01
			} else {
				bat[2] = 0x00
			}
			s.emitRegister(protocol.BatteryLevelRegister, bat)
		}

	case protocol.SetUpdateSettingRegister: // 0x0f
		if len(msg.Data) >= 2 {
			if (msg.Data[0] == 0xfe && msg.Data[1] == 0x00) || (msg.Data[0] == 0xff && msg.Data[1] == 0x01) {
				s.stagedSettings = make(map[byte]byte)
				s.car.Settings.Clear()
				for _, hexFrame := range defaultSettings {
					if raw, err := hex.DecodeString(hexFrame); err == nil {
						_ = s.car.Settings.FromRegister(raw)
					}
				}
			} else {
				s.stagedSettings[msg.Data[0]] = msg.Data[1]
			}
		}

	case protocol.SetCommitSettingsRegister: // 0x0e
		for id, val := range s.stagedSettings {
			if frame, ok := s.car.Settings.UpdateValue(id, val); ok {
				s.SendMsg(protocol.NewMessage(protocol.CmdInResp, protocol.SettingsRegister, false, frame))
			}
		}
		s.stagedSettings = make(map[byte]byte)
		s.emitRegister(0x0f, []byte{0x00})

	case protocol.SetWiFiAlwaysOnRegister: // 0x11
		if len(msg.Data) >= 1 {
			var v byte
			if msg.Data[0] == 0x01 {
				v = 0x01
			}
			s.emitRegister(protocol.WiFiAnyConnRegister, []byte{v})
		}

	case protocol.SetAckPreACTermRegister: // 0x13
		s.emitRegister(protocol.PreACStateRegister, []byte{byte(protocol.PreACOff), 0x00, 0x00})

	case protocol.SetChangeSSIDRegister: // 0x14
		if len(msg.Data) == 32 {
			s.emitRegister(protocol.WIFISSIDRegister, msg.Data)
		}

	case protocol.SetChargeTimerMasterReg: // 0x17
		if len(msg.Data) >= 1 {
			state := byte(0x11)
			if msg.Data[0] == 0x02 {
				state = 0x12
			}
			s.emitRegister(protocol.ChargeTimerStateRegister, []byte{state})
			s.emitRegister(protocol.ChargeTimerOverrideReg, []byte{msg.Data[0]})
		}

	case protocol.SetChargeTimersMY18Reg: // 0x19 (21B)
		var sched protocol.ChargeSchedule
		if err := sched.DecodeWrite0x19(msg.Data); err == nil {
			s.emitRegister(protocol.ChargeTimersRegister, sched.EncodeRead0x04())
		}

	case protocol.SetClimateTimersMY18Reg: // 0x1a (16B)
		var sched protocol.ClimateSchedule
		if err := sched.DecodeWrite0x1a(msg.Data); err == nil {
			s.emitRegister(protocol.ClimateTimersRegister, sched.EncodeRead0x05())
			acByte := byte(0x02)
			switch sched.Mode {
			case "cool":
				acByte = 0x01
			case "windscreen":
				acByte = 0x03
			}
			s.emitRegister(protocol.ACModeRegister, []byte{acByte})
		}

	case protocol.SetACModeRegisterMY18: // 0x1b (4B: [state, mode, dur, delay])
		if len(msg.Data) >= 4 {
			state := msg.Data[0]
			mode := msg.Data[1] & 0x0f
			dur := msg.Data[2] & 0x03
			delay := msg.Data[3] & 0x03
			if state == 0x02 && mode >= 1 && mode <= 3 {
				s.emitRegister(protocol.ACModeRegister, []byte{mode | (dur << 4)})
				if delay > 0 {
					s.emitRegister(protocol.ACOperStatusRegister, []byte{0x00, 0x00, dur, delay, 0x01})
					s.emitRegister(protocol.PreACStateRegister, []byte{byte(protocol.PreACStarting), 0x00, 0x00})
				} else {
					durMin := 10 + int(dur)*10
					stopAt := time.Now().Add(time.Duration(durMin) * time.Minute)
					preAC := &protocol.RegisterPreACState{
						State:         protocol.PreACOn,
						HasTargetTime: true,
						TargetDay:     byte(stopAt.Weekday()),
						TargetHour:    stopAt.Hour(),
						TargetMinute:  stopAt.Minute(),
					}
					s.emitRegister(protocol.ACOperStatusRegister, []byte{0x00, 0x01, dur, 0x00, 0x00})
					s.emitRegister(protocol.PreACStateRegister, preAC.Encode().Data)
				}
			} else {
				s.emitRegister(protocol.ACOperStatusRegister, []byte{0x00, 0x00, 0x00, 0x00, 0x00})
				s.emitRegister(protocol.PreACStateRegister, []byte{byte(protocol.PreACOff), 0x00, 0x00})
			}
		}

	case protocol.SetWiFiChannelRegister: // 0x1c
		if len(msg.Data) >= 1 && msg.Data[0] >= 1 && msg.Data[0] <= 13 {
			s.emitRegister(protocol.WiFiChannelRegister, []byte{msg.Data[0]})
		}
	}
}

func (s *Connection) sendNextRegister() {
	if s.settingsSender != nil {
		if setting, ok := <-s.settingsSender.C; ok {
			s.SendMsg(setting)
		} else {
			s.settingsSender = nil
		}
		return
	}
	if s.registerIndex < 0 {
		return
	}
	regs := s.car.SnapshotRegisters()
	if s.registerIndex >= len(regs) {
		s.registerIndex = -1
		s.settingsSender = s.car.Settings.NewSender()
		s.settingsSender.Start()
		return
	}
	msg := regs[s.registerIndex].Encode()
	msg.Type = protocol.CmdInResp
	msg.Ack = protocol.Request
	s.SendMsg(msg)

	s.registerIndex++
	if s.registerIndex >= len(regs) {
		s.registerIndex = -1
		log.Debug("Finished sending registers, sending settings")
		s.settingsSender = s.car.Settings.NewSender()
		s.settingsSender.Start()
	}
}

// Generate and send new key request.
func (s *Connection) rekey() {
	reqCmd := s.startReqCmd
	if reqCmd == 0 {
		reqCmd = protocol.CmdInMy18StartReq
	}
	if s.state == conRegisterStart {
		s.SendMsg(protocol.NewMessage(reqCmd, 0x1, true, []byte{0x0}))
	}
	data := s.key.GenerateProposal()
	s.SendMsg(protocol.NewMessage(reqCmd, 0x1, false, append(data, 0x1)))
	s.key.State = protocol.SecurityKeyProposed
}
