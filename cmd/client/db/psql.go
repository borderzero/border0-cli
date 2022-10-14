package db

import (
	"fmt"
	"strings"

	"github.com/borderzero/border0-cli/client/preference"
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

		certChainPath, cleanup, err := client.DownloadCertificateChain(hostname)
		if err != nil {
			cleanup()
			return err
		}
		defer cleanup()

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

		_, _, crtPath, keyPath, port, err := client.GetOrgCert(hostname)
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

		return client.ExecCommand("psql",
			"--host", hostname,
			"--port", fmt.Sprint(port),
			strings.Join([]string{
				fmt.Sprintf("dbname=%s", dbName),
				"sslmode=verify-full",
				fmt.Sprintf("sslkey=%s", keyPath),
				fmt.Sprintf("sslcert=%s", crtPath),
				fmt.Sprintf("sslrootcert=%s", certChainPath),
			}, " "),
		)
	},
}
