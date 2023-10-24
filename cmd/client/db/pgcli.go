package db

import (
	"fmt"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var pgcliCmd = &cobra.Command{
	Use:   "db:pgcli",
	Short: "Connect to a database socket with pgcli client",
	RunE: func(cmd *cobra.Command, args []string) error {
		pickedHost, err := client.PickHost(hostname, enum.DatabaseSocket)
		if err != nil {
			return err
		}
		hostname = pickedHost.Hostname()

		certChainPath, err := client.DownloadCertificateChain(hostname)
		if err != nil {
			return err
		}

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
		socketPref.DatabaseClient = "pgcli"
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

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
			info.Port, err = client.StartConnectorAuthListener(fmt.Sprintf("%s:%d", hostname, info.Port), info.SetupTLSCertificate(), info.CaCertificate, 0, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled)
			if err != nil {
				fmt.Println("ERROR: could not setup listener:", err)
				return err
			}

			hostname = "127.0.0.1"
		}

		sslmode := "verify-full"
		if info.ConnectorAuthenticationEnabled {
			sslmode = "verify-ca"
		}

		return client.ExecCommand("pgcli", fmt.Sprintf(
			"postgres://:@%[1]s:%[2]d/%[3]s?sslmode=%[7]s&sslkey=%[4]s&sslcert=%[5]s&sslrootcert=%[6]s",
			hostname, info.Port, dbName, info.PrivateKeyPath, info.CertificatePath, certChainPath, sslmode,
		))
	},
}
