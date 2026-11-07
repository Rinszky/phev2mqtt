package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// startAckDrain drains cl.Recv in the background, ACKs car register notifications,
// populates cl.Settings on 0x16 frames, and invokes onMsg if provided.
func startAckDrain(cl *client.Client, onMsg func(*protocol.PhevMessage)) {
	go func() {
		for msg := range cl.Recv {
			if msg.Type == protocol.CmdInResp && msg.Ack == protocol.Request {
				if msg.Register == protocol.SettingsRegister {
					_ = cl.Settings.FromRegister(msg.Data)
				}
				if onMsg != nil {
					onMsg(msg)
				}
				cl.SendMessage(&protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: msg.Register,
					Ack:      protocol.Ack,
					Xor:      msg.Xor,
					Data:     []byte{0x0},
				})
			}
		}
	}()
}

var synctimeCmd = &cobra.Command{
	Use:   "synctime",
	Short: "Synchronize the vehicle's real-time clock with host time",
	Long: `Connects to the vehicle, displays its current real-time clock (register 0x12),
and synchronizes it to the local system clock (or --time override) via register 0x05.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		address := viper.GetString("address")
		overrideTime, _ := cmd.Flags().GetString("time")

		target := time.Now()
		hasOverride := false
		if overrideTime != "" {
			parsed, err := time.ParseInLocation("2006-01-02 15:04:05", overrideTime, time.Local)
			if err != nil {
				return fmt.Errorf("invalid --time %q (expected 'YYYY-MM-DD HH:MM:SS'): %w", overrideTime, err)
			}
			target = parsed
			hasOverride = true
		}

		cl, err := client.New(client.AddressOption(address))
		if err != nil {
			return err
		}
		if err := cl.Connect(); err != nil {
			return err
		}
		defer cl.Close()

		var mu sync.Mutex
		var carClock *time.Time
		clockCh := make(chan time.Time, 4)

		if err := cl.Start(); err != nil {
			return err
		}

		startAckDrain(cl, func(msg *protocol.PhevMessage) {
			if rt, ok := msg.Reg.(*protocol.RegisterTime); ok {
				mu.Lock()
				t := rt.Time
				carClock = &t
				mu.Unlock()
				select {
				case clockCh <- rt.Time:
				default:
				}
			}
		})

		// Brief pause to receive initial 0x12 notification if still streaming.
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		before := carClock
		mu.Unlock()
		if before != nil {
			fmt.Printf("Current vehicle clock: %s\n", before.Format("2006-01-02 15:04:05"))
		}

		if !hasOverride {
			target = time.Now()
		}
		log.Infof("Syncing vehicle clock to %s", target.Format("2006-01-02 15:04:05"))
		// Drain any prior buffered clock event before sending sync.
		for len(clockCh) > 0 {
			<-clockCh
		}
		if err := cl.SetRegister(protocol.SetSyncTimeRegister, protocol.EncodeSyncTime(target)); err != nil {
			return fmt.Errorf("failed to sync vehicle clock: %w", err)
		}

		select {
		case updated := <-clockCh:
			fmt.Printf("Updated vehicle clock: %s\n", updated.Format("2006-01-02 15:04:05"))
		case <-time.After(3 * time.Second):
			fmt.Printf("Sync command acknowledged (target: %s)\n", target.Format("2006-01-02 15:04:05"))
		}
		return nil
	},
}

func init() {
	clientCmd.AddCommand(synctimeCmd)
	synctimeCmd.Flags().String("time", "", "Optional explicit local time 'YYYY-MM-DD HH:MM:SS' (defaults to host clock)")
}
