/*
Copyright © 2021 Ben Buxton <bbuxton@gmail.com>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"encoding/hex"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// watchCmd represents the watch command
var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Connect to Phev and watch incoming updates",
	Long: `Connects to the Phev and watches for incoming register
updates, displaying them in real-time.`,
	Run: Run,
}

func Run(cmd *cobra.Command, args []string) {
	address, _ := cmd.Flags().GetString("address")
	waitDur, _ := cmd.Flags().GetDuration("wait")

	cl, err := client.New(client.AddressOption(address))
	if err != nil {
		panic(err)
	}

	if err := cl.Connect(); err != nil {
		panic(err)
	}
	defer cl.Close()

	if err := cl.Start(); err != nil {
		panic(err)
	}

	var waitCh <-chan time.Time
	if waitDur > 0 {
		timer := time.NewTimer(waitDur)
		defer timer.Stop()
		waitCh = timer.C
	}

	var registers = map[byte]string{}

	for {
		select {
		case <-waitCh:
			log.Infof("Watch duration (%s) elapsed, disconnecting.", waitDur)
			return
		case m, ok := <-cl.Recv:
			if !ok {
				log.Infof("Connection closed.")
				return
			}
			switch m.Type {
			case protocol.CmdInResp:
				if m.Ack != protocol.Request {
					break
				}
				dataStr := hex.EncodeToString(m.Data)
				if data := registers[m.Register]; data != dataStr || m.Register == protocol.SettingsRegister {
					log.Infof("%%PHEV_REG_UPDATE%% %02x: %s -> %s", m.Register, data, dataStr)
					registers[m.Register] = dataStr
					if m.Reg != nil {
						if _, ok := m.Reg.(*protocol.RegisterGeneric); !ok {
							log.Infof("%%PHEV_REG_UPDATE%% %02x: [%s]", m.Register, m.Reg.String())
						}
					}
				}
				_ = cl.SendMessage(&protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: m.Register,
					Ack:      protocol.Ack,
					Xor:      m.Xor,
					Data:     []byte{0x0},
				})
			}
		}
	}
}

func init() {
	clientCmd.AddCommand(watchCmd)

	watchCmd.Flags().DurationP("wait", "w", 0, "How long to hold connection open for (0 = indefinitely)")
}
