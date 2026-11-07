package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var timersCmd = &cobra.Command{
	Use:   "timers",
	Short: "View or configure MY18+ climate (heating/cooling) and charging schedules",
	Long: `Inspect and update the 5 climate pre-conditioning timers (registers 0x05 / 0x1a / 0x03)
and 5 charging timers (registers 0x04 / 0x19 / 0x17) on the vehicle.`,
}

type loadedSchedules struct {
	mu      sync.Mutex
	climate protocol.ClimateSchedule
	charge  protocol.ChargeSchedule
	gotClim bool
	gotChg  bool
}

func connectAndLoadTimers(wait time.Duration) (*client.Client, *loadedSchedules, error) {
	address := viper.GetString("address")
	cl, err := client.New(client.AddressOption(address))
	if err != nil {
		return nil, nil, err
	}
	if err := cl.Connect(); err != nil {
		return nil, nil, err
	}
	if err := cl.Start(); err != nil {
		_ = cl.Close()
		return nil, nil, err
	}

	st := &loadedSchedules{
		climate: protocol.NewDefaultClimateSchedule(),
		charge:  protocol.NewDefaultChargeSchedule(),
	}
	readyCh := make(chan struct{}, 1)

	startAckDrain(cl, func(msg *protocol.PhevMessage) {
		st.mu.Lock()
		switch reg := msg.Reg.(type) {
		case *protocol.RegisterClimateTimers:
			mode := st.climate.Mode
			st.climate = reg.Schedule
			if mode != "" {
				st.climate.Mode = mode
			}
			st.gotClim = true
		case *protocol.RegisterACMode:
			if reg.Mode == "cool" || reg.Mode == "heat" || reg.Mode == "windscreen" {
				st.climate.Mode = reg.Mode
			}
		case *protocol.RegisterChargeTimers:
			master := st.charge.MasterEnabled
			st.charge = reg.Schedule
			st.charge.MasterEnabled = master
			st.gotChg = true
		case *protocol.RegisterChargeTimerState:
			if reg.Reg == protocol.ChargeTimerStateRegister {
				st.charge.MasterEnabled = reg.Enabled
			}
		}
		both := st.gotClim && st.gotChg
		st.mu.Unlock()
		if both {
			select {
			case readyCh <- struct{}{}:
			default:
			}
		}
	})

	_ = cl.SetRegister(protocol.SetRequestUpdateRegister, []byte{0x03})

	select {
	case <-readyCh:
		time.Sleep(250 * time.Millisecond)
	case <-time.After(wait):
	}
	return cl, st, nil
}

var timersListCmd = &cobra.Command{
	Use:          "list",
	Short:        "Display all 5 climate timers and 5 charge timers",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		wait, _ := cmd.Flags().GetDuration("wait")
		cl, st, err := connectAndLoadTimers(wait)
		if err != nil {
			return err
		}
		defer cl.Close()

		st.mu.Lock()
		clim := st.climate
		chg := st.charge
		st.mu.Unlock()

		fmt.Printf("Climate Schedule (Master Enabled: %t, Mode: %s)\n", clim.MasterEnabled, clim.Mode)
		for i := 0; i < 5; i++ {
			fmt.Printf("  %s\n", clim.Timers[i].Summary())
		}
		fmt.Println()
		fmt.Printf("Charge Schedule (Master Enabled: %t)\n", chg.MasterEnabled)
		for i := 0; i < 5; i++ {
			fmt.Printf("  %s\n", chg.Timers[i].Summary())
		}
		return nil
	},
}

var timersSetClimateCmd = &cobra.Command{
	Use:          "set-climate",
	Short:        "Configure or clear a climate (heating/cooling/windscreen) timer slot (1..5)",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		slotNum, _ := cmd.Flags().GetInt("slot")
		if slotNum < 1 || slotNum > 5 {
			return fmt.Errorf("--slot must be between 1 and 5")
		}
		clearSlot, _ := cmd.Flags().GetBool("clear")
		timeStr, _ := cmd.Flags().GetString("time")
		dur, _ := cmd.Flags().GetInt("duration")
		daysStr, _ := cmd.Flags().GetString("days")
		modeStr, _ := cmd.Flags().GetString("mode")
		enabled, _ := cmd.Flags().GetBool("enabled")
		wait, _ := cmd.Flags().GetDuration("wait")

		cl, st, err := connectAndLoadTimers(wait)
		if err != nil {
			return err
		}
		defer cl.Close()

		st.mu.Lock()
		sched := st.climate
		st.mu.Unlock()

		if clearSlot {
			sched.Timers[slotNum-1] = protocol.DefaultClimateTimerSlot(slotNum)
		} else {
			slot := sched.Timers[slotNum-1]
			slot.Index = slotNum
			if cmd.Flags().Changed("time") {
				h, mn, cfg, err := protocol.ParseTimerHHMM(timeStr)
				if err != nil {
					return err
				}
				if !cfg {
					slot = protocol.DefaultClimateTimerSlot(slotNum)
				} else {
					slot.Hour = h
					slot.Minute = mn
					slot.Configured = true
				}
			}
			if cmd.Flags().Changed("duration") {
				if dur != 10 && dur != 20 && dur != 30 {
					return fmt.Errorf("--duration must be 10, 20, or 30")
				}
				slot.Duration = dur
				slot.Configured = true
			}
			if cmd.Flags().Changed("days") {
				mask, err := protocol.ParseDaysMask(daysStr)
				if err != nil {
					return err
				}
				slot.DaysMask = mask
				slot.Configured = mask != protocol.DaysNone
			}
			if cmd.Flags().Changed("enabled") {
				slot.Enabled = enabled
				if enabled {
					slot.Configured = true
				}
			} else if slot.Configured {
				slot.Enabled = true
			}
			slot.EnsureValidDefaults()
			sched.Timers[slotNum-1] = slot
		}

		if modeStr != "" {
			if modeStr != "heat" && modeStr != "cool" && modeStr != "windscreen" {
				return fmt.Errorf("--mode must be heat, cool, or windscreen")
			}
			sched.Mode = modeStr
		}

		if err := cl.SetRegister(protocol.SetClimateTimersMY18Reg, sched.EncodeWrite0x1a()); err != nil {
			return fmt.Errorf("failed writing climate timers (0x1a): %w", err)
		}
		masterByte := byte(0x01)
		if sched.AnyEnabled() {
			masterByte = 0x02
		}
		if err := cl.SetRegister(protocol.SetClimateTimerMasterReg, []byte{masterByte}); err != nil {
			return fmt.Errorf("failed updating climate timer master (0x03): %w", err)
		}

		fmt.Printf("Saved Climate %s (Mode: %s, Master: %t)\n",
			sched.Timers[slotNum-1].Summary(), sched.Mode, sched.AnyEnabled())
		return nil
	},
}

var timersSetChargeCmd = &cobra.Command{
	Use:          "set-charge",
	Short:        "Configure or clear a battery charging timer slot (1..5)",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		slotNum, _ := cmd.Flags().GetInt("slot")
		if slotNum < 1 || slotNum > 5 {
			return fmt.Errorf("--slot must be between 1 and 5")
		}
		clearSlot, _ := cmd.Flags().GetBool("clear")
		startStr, _ := cmd.Flags().GetString("start")
		endStr, _ := cmd.Flags().GetString("end")
		daysStr, _ := cmd.Flags().GetString("days")
		fullCharge, _ := cmd.Flags().GetBool("full-charge")
		enabled, _ := cmd.Flags().GetBool("enabled")
		wait, _ := cmd.Flags().GetDuration("wait")

		cl, st, err := connectAndLoadTimers(wait)
		if err != nil {
			return err
		}
		defer cl.Close()

		st.mu.Lock()
		sched := st.charge
		st.mu.Unlock()

		if clearSlot {
			sched.Timers[slotNum-1] = protocol.DefaultChargeTimerSlot(slotNum)
		} else {
			slot := sched.Timers[slotNum-1]
			slot.Index = slotNum
			if cmd.Flags().Changed("start") {
				h, mn, cfg, err := protocol.ParseTimerHHMM(startStr)
				if err != nil {
					return err
				}
				if !cfg {
					slot = protocol.DefaultChargeTimerSlot(slotNum)
				} else {
					slot.StartHour = h
					slot.StartMinute = mn
					slot.Configured = true
				}
			}
			if cmd.Flags().Changed("end") {
				h, mn, cfg, err := protocol.ParseTimerHHMM(endStr)
				if err != nil {
					return err
				}
				if cfg {
					slot.EndHour = h
					slot.EndMinute = mn
					slot.Configured = true
				}
			}
			if cmd.Flags().Changed("days") {
				mask, err := protocol.ParseDaysMask(daysStr)
				if err != nil {
					return err
				}
				slot.DaysMask = mask
				slot.Configured = mask != protocol.DaysNone
			}
			if cmd.Flags().Changed("full-charge") {
				slot.FullCharge = fullCharge
				slot.Configured = true
			}
			if cmd.Flags().Changed("enabled") {
				slot.Enabled = enabled
				if enabled {
					slot.Configured = true
				}
			} else if slot.Configured {
				slot.Enabled = true
			}
			slot.EnsureValidDefaults()
			sched.Timers[slotNum-1] = slot
		}

		if err := cl.SetRegister(protocol.SetChargeTimersMY18Reg, sched.EncodeWrite0x19()); err != nil {
			return fmt.Errorf("failed writing charge timers (0x19): %w", err)
		}
		masterByte := byte(0x01)
		if sched.AnyEnabled() {
			masterByte = 0x02
		}
		if err := cl.SetRegister(protocol.SetChargeTimerMasterReg, []byte{masterByte}); err != nil {
			return fmt.Errorf("failed updating charge timer master (0x17): %w", err)
		}

		fmt.Printf("Saved Charge %s (Master: %t)\n",
			sched.Timers[slotNum-1].Summary(), sched.AnyEnabled())
		return nil
	},
}

func init() {
	clientCmd.AddCommand(timersCmd)
	timersCmd.PersistentFlags().Duration("wait", 4*time.Second, "Maximum time to wait for current schedules from car")

	timersSetClimateCmd.Flags().Int("slot", 1, "Timer slot number (1..5)")
	timersSetClimateCmd.Flags().String("time", "07:00", "Start time HH:MM (10-min increments)")
	timersSetClimateCmd.Flags().Int("duration", 20, "Duration in minutes (10, 20, or 30)")
	timersSetClimateCmd.Flags().String("days", "Weekdays", "Days of week (e.g. Everyday, Weekdays, Weekend, Mon,Wed,Fri, H,K,Sze,Cs,P)")
	timersSetClimateCmd.Flags().String("mode", "", "Optional scheduled climate mode (heat, cool, windscreen)")
	timersSetClimateCmd.Flags().Bool("enabled", true, "Whether the timer slot is enabled")
	timersSetClimateCmd.Flags().Bool("clear", false, "Clear/unset this timer slot")

	timersSetChargeCmd.Flags().Int("slot", 1, "Timer slot number (1..5)")
	timersSetChargeCmd.Flags().String("start", "23:00", "Start time HH:MM (10-min increments)")
	timersSetChargeCmd.Flags().String("end", "06:00", "End time HH:MM (10-min increments)")
	timersSetChargeCmd.Flags().String("days", "Everyday", "Days of week (e.g. Everyday, Weekdays, Weekend, Mon,Tue,Wed)")
	timersSetChargeCmd.Flags().Bool("full-charge", true, "Continue charging until battery is 100% full")
	timersSetChargeCmd.Flags().Bool("enabled", true, "Whether the timer slot is enabled")
	timersSetChargeCmd.Flags().Bool("clear", false, "Clear/unset this timer slot")

	timersCmd.AddCommand(timersListCmd, timersSetClimateCmd, timersSetChargeCmd)
}
