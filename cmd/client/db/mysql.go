package db

import (
	"fmt"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var mysqlCmd = &cobra.Command{
	Use:   "db:mysql",
	Short: "Connect to a database socket with MySQL client",
	RunE: func(cmd *cobra.Command, args []string) error {
		var err error
		pickedHost, err := client.PickHost(hostname, enum.DatabaseSocket)
		if err != nil {
			return err
		}
		hostname = pickedHost.Hostname()

		// Let's read preferences from the config file
		pref, err := preference.Read()
		if err != nil {
			fmt.Println("WARNING: could not read preference file:", err)
		}
		socketPref := preference.NewDatabaseSocket(hostname)

		var suggestedDBName string

		dbName := dbNameFrom(args)
		if dbName == "" {
			suggestedSocket := pref.GetOrSuggestSocket(hostname, enum.DatabaseSocket)
			if preference.Found(suggestedSocket) {
				suggestedDBName = suggestedSocket.DatabaseName
				socketPref = suggestedSocket
			}
		}

		dbName, err = client.EnterDBName(dbName, suggestedDBName)
		if err != nil {
			return err
		}

		socketPref.DatabaseName = dbName
		socketPref.DatabaseClient = "mysql"
		pref.SetSocket(socketPref)

		info, err := client.GetResourceInfo(logger.Logger, hostname)
		if err != nil {
			return err
		}

		persistPreference := func() {
			// persist preference to json file
			if err == nil {
				if err := preference.Write(pref); err != nil {
					fmt.Println("WARNING: could not update preference file:", err)
				}
			}
		}
		// make sure we will persist preference on successful connection to socket
		defer persistPreference()
		client.OnInterruptDo(persistPreference)

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled || wsProxy != "" {
			info.Port, err = client.StartConnectorAuthListener(hostname, info.Port, info.SetupTLSCertificate(), info.CaCertificate, 0, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled, wsProxy)
			if err != nil {
				fmt.Println("ERROR: could not setup listener:", err)
				return err
			}

			hostname = "localhost"
		}

		err = client.ExecCommand("mysql", []string{
			"-h", hostname,
			"-P", fmt.Sprint(info.Port),
			"--protocol", "TCP",
			"--ssl-cert", info.CertificatePath,
			"--ssl-key", info.PrivateKeyPath,
			dbName,
		}...)
		return err
	},
}
