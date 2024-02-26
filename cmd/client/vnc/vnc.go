package vnc

import (
	"fmt"

	"github.com/borderzero/border0-cli/cmd/client/utils"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var (
	hostname          string
	localListenerPort int
)

// clientVncCmd represents the client vnc command
var clientVncCmd = &cobra.Command{
	Use:               "vnc",
	Short:             "Connect to a VNC socket",
	ValidArgsFunction: client.AutocompleteHost,
	RunE: func(cmd *cobra.Command, args []string) error {

		if len(args) > 0 {
			hostname = args[0]
		}

		if hostname == "" {
			// TODO(devs): Remove TLSSocket once we have exposed CRUD for VNC sockets in the admin portal.
			pickedHost, err := client.PickHost(hostname, enum.VNCSocket, enum.TLSSocket)
			if err != nil {
				return fmt.Errorf("failed to pick host: %v", err)
			}
			hostname = pickedHost.Hostname()
		}

		return utils.StartLocalProxyAndOpenClient(cmd, args, "vnc", hostname, localListenerPort)
	},
}

func AddCommandsTo(client *cobra.Command) {
	client.AddCommand(clientVncCmd)

	clientVncCmd.Flags().StringVarP(&hostname, "service", "", "", "The Border0 service identifier")
	clientVncCmd.Flags().IntVarP(&localListenerPort, "local-listener-port", "l", 0, "Local listener port number")
}
