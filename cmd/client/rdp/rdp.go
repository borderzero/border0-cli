package rdp

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
	wsProxy           string
)

// clientRdpCmd represents the client rdp command
var clientRdpCmd = &cobra.Command{
	Use:               "rdp",
	Short:             "Connect to an RDP socket",
	ValidArgsFunction: client.AutocompleteHost,
	RunE: func(cmd *cobra.Command, args []string) error {

		if len(args) > 0 {
			hostname = args[0]
		}

		if hostname == "" {
			pickedHost, err := client.PickHost(hostname, enum.RDPSocket)
			if err != nil {
				return fmt.Errorf("failed to pick host: %v", err)
			}
			hostname = pickedHost.Hostname()
		}

		return utils.StartLocalProxyAndOpenClient(cmd, args, "rdp", hostname, localListenerPort, wsProxy)
	},
}

func AddCommandsTo(client *cobra.Command) {
	client.AddCommand(clientRdpCmd)

	clientRdpCmd.Flags().StringVarP(&hostname, "service", "", "", "The Border0 service identifier")
	clientRdpCmd.Flags().IntVarP(&localListenerPort, "local-listener-port", "l", 0, "Local listener port number")
	clientRdpCmd.Flags().StringVarP(&wsProxy, "wsproxy", "w", "", "websocket proxy")
}
