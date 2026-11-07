package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "List or modify ETACS / AC vehicle customization settings",
	Long: `Inspect and update the vehicle's built-in ETACS and air-conditioning customization
settings (register 0x16 / 0x0f / 0x0e), such as auto-fold mirrors, auto-relock timer,
horn/turn-signal answerback, wiper intervals, headlight cut-out, and AC recirculation.`,
}

func connectAndLoadSettings(wait time.Duration) (*client.Client, error) {
	address := viper.GetString("address")
	cl, err := client.New(client.AddressOption(address))
	if err != nil {
		return nil, err
	}
	if err := cl.Connect(); err != nil {
		return nil, err
	}
	if err := cl.Start(); err != nil {
		_ = cl.Close()
		return nil, err
	}

	lastSetting := make(chan struct{}, 1)
	startAckDrain(cl, func(msg *protocol.PhevMessage) {
		if msg.Register == protocol.SettingsRegister {
			select {
			case lastSetting <- struct{}{}:
			default:
			}
		}
	})

	// Request full update in case initial dump already finished before drain attached,
	// then wait until settings settle or timeout expires.
	_ = cl.SetRegister(protocol.SetRequestUpdateRegister, []byte{0x03})

	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	idle := time.NewTimer(1500 * time.Millisecond)
	defer idle.Stop()
	seenAny := false

	for {
		select {
		case <-lastSetting:
			seenAny = true
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(600 * time.Millisecond)
		case <-idle.C:
			if seenAny && len(cl.Settings.SupportedSettings()) > 0 {
				return cl, nil
			}
			idle.Reset(500 * time.Millisecond)
		case <-deadline.C:
			return cl, nil
		}
	}
}

var settingsListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List all supported vehicle customization settings and options",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		wait, _ := cmd.Flags().GetDuration("wait")
		showAll, _ := cmd.Flags().GetBool("all")

		cl, err := connectAndLoadSettings(wait)
		if err != nil {
			return err
		}
		defer cl.Close()

		var list []*protocol.VehicleSetting
		if showAll {
			list = cl.Settings.AllSettings()
		} else {
			list = cl.Settings.SupportedSettings()
		}
		if len(list) == 0 {
			fmt.Println("No vehicle settings received (try increasing --wait).")
			return nil
		}

		fmt.Printf("%-5s  %-30s  %-26s  %s\n", "ID", "SLUG", "CURRENT VALUE", "SUPPORTED OPTIONS")
		fmt.Println(strings.Repeat("-", 105))
		for _, vs := range list {
			opts := strings.Join(vs.SupportedOptions(), " | ")
			if !vs.Supported() {
				opts = "(unsupported on this trim)"
			}
			fmt.Printf("0x%02x   %-30s  %-26s  [%s]\n", vs.ID, vs.Slug, vs.ValueName(), opts)
		}
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:          "set <slug_or_id> <option>",
	Short:        "Update and commit a vehicle customization setting",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		wait, _ := cmd.Flags().GetDuration("wait")
		cl, err := connectAndLoadSettings(wait)
		if err != nil {
			return err
		}
		defer cl.Close()

		vs, ok := cl.Settings.GetBySlug(args[0])
		if !ok || !vs.Supported() {
			return fmt.Errorf("setting %q is unknown or not supported on this vehicle", args[0])
		}
		optIdx, err := vs.ResolveOption(args[1])
		if err != nil {
			return err
		}

		if err := cl.SetRegister(protocol.SetUpdateSettingRegister, protocol.EncodeUpdateSetting(vs.ID, optIdx)); err != nil {
			return fmt.Errorf("failed staging setting 0x%02x: %w", vs.ID, err)
		}
		if err := cl.SetRegister(protocol.SetCommitSettingsRegister, protocol.EncodeCommitSettings()); err != nil {
			return fmt.Errorf("failed committing setting 0x%02x: %w", vs.ID, err)
		}
		fmt.Printf("Updated 0x%02x (%s) -> %s (index %d)\n", vs.ID, vs.Slug, vs.OptionLabel(optIdx), optIdx)
		return nil
	},
}

var settingsResetCmd = &cobra.Command{
	Use:          "reset",
	Short:        "Factory-reset all vehicle customization settings",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		address := viper.GetString("address")
		cl, err := client.New(client.AddressOption(address))
		if err != nil {
			return err
		}
		if err := cl.Connect(); err != nil {
			return err
		}
		defer cl.Close()
		if err := cl.Start(); err != nil {
			return err
		}
		startAckDrain(cl, nil)

		if err := cl.SetRegister(protocol.SetUpdateSettingRegister, protocol.EncodeFactoryResetSettings()); err != nil {
			return fmt.Errorf("failed staging settings reset: %w", err)
		}
		if err := cl.SetRegister(protocol.SetCommitSettingsRegister, protocol.EncodeCommitSettings()); err != nil {
			return fmt.Errorf("failed committing settings reset: %w", err)
		}
		fmt.Println("Vehicle customization settings reset to factory defaults.")
		return nil
	},
}

func init() {
	clientCmd.AddCommand(settingsCmd)
	settingsCmd.PersistentFlags().Duration("wait", 6*time.Second, "Maximum time to wait for settings dump from car")
	settingsListCmd.Flags().Bool("all", false, "Show unsupported setting slots as well")
	settingsCmd.AddCommand(settingsListCmd, settingsSetCmd, settingsResetCmd)
}
