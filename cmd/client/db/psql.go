package db

import (
	"fmt"
	"strings"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var psqlCmd = &cobra.Command{
	Use:   "db:psql",
	Short: "Connect to a database socket with psql client",
	RunE: func(cmd *cobra.Command, args []string) error {
		var err error

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
		socketPref.DatabaseClient = "psql"
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
			info.Port, err = client.StartConnectorAuthListener(fmt.Sprintf("%s:%d", hostname, info.Port), info.SetupTLSCertificate(), 0, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled)
			if err != nil {
				fmt.Println("ERROR: could not setup listener:", err)
				return err
			}

			hostname = "localhost"
		}

		sslmode := "verify-full"
		if info.ConnectorAuthenticationEnabled {
			sslmode = "verify-ca"
		}

		return client.ExecCommand("psql",
			"--host", hostname,
			"--port", fmt.Sprint(info.Port),
			strings.Join([]string{
				fmt.Sprintf("dbname=%s", dbName),
				fmt.Sprintf("sslmode=%s", sslmode),
				fmt.Sprintf("sslkey=%s", info.PrivateKeyPath),
				fmt.Sprintf("sslcert=%s", info.CertificatePath),
				fmt.Sprintf("sslrootcert=%s", certChainPath),
			}, " "),
		)
	},
}
