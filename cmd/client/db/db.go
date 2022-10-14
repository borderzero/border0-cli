package db

import (
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var (
	hostname string
)

func AddCommandsTo(client *cobra.Command) {
	addOneCommandTo(dbCmd, client)
	addOneCommandTo(mysqlCmd, client)
	addOneCommandTo(mycliCmd, client)
	addOneCommandTo(mysqlWorkbenchCmd, client)
	addOneCommandTo(dbeaverCmd, client)
	addOneCommandTo(psqlCmd, client)
	addOneCommandTo(pgcliCmd, client)
}

func addOneCommandTo(cmdToAdd, cmdAddedTo *cobra.Command) {
	cmdToAdd.Flags().StringVarP(&hostname, "host", "", "", "Socket target host")
	cmdAddedTo.AddCommand(cmdToAdd)
}

func dbNameFrom(args []string) string {
	var dbName string
	if len(args) > 0 {
		dbName = args[0]
	}
	return dbName
}

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Pick a socket host and connect to it as a database",
	RunE: func(cmd *cobra.Command, args []string) error {
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

		var suggestedDBName, suggestedDBClient string

		dbName := dbNameFrom(args)
		if dbName == "" {
			suggestedSocket := pref.GetOrSuggestSocket(hostname, enum.DatabaseSocket)
			if preference.Found(suggestedSocket) {
				suggestedDBName = suggestedSocket.DatabaseName
				suggestedDBClient = suggestedSocket.DatabaseClient
			}
		}

		dbName, err = client.EnterDBName(dbName, suggestedDBName)
		if err != nil {
			return err
		}

		var (
			dbClient            string
			dbClients           []string
			dbClientsMySQL      = []string{"mysql", "mysqlworkbench", "mycli", "dbeaver"}
			dbClientsPostgreSQL = []string{"psql", "pgcli"}
		)
		switch pickedHost.DatabaseType {
		case "mysql":
			dbClients = []string{"mysql", "mysqlworkbench", "mycli", "dbeaver"}
		case "postgres":
			dbClients = []string{"psql", "pgcli"}
		default:
			dbClients = append(dbClientsMySQL, dbClientsPostgreSQL...)
		}
		prompt := &survey.Select{
			Message: "choose a client:",
			Options: dbClients,
		}
		if suggestedDBClient != "" {
			for _, oneDBClient := range dbClients {
				if oneDBClient == suggestedDBClient {
					prompt.Default = suggestedDBClient
				}
			}
		}
		if err := survey.AskOne(prompt, &dbClient); err != nil {
			return err
		}

		cmdToCall := "db:" + dbClient
		foundCmd, _, _ := cmd.Parent().Find([]string{cmdToCall})
		if foundCmd.Use != cmdToCall || foundCmd.RunE == nil {
			return fmt.Errorf("couldn't find client subcommand %s", cmdToCall)
		}
		if len(args) == 0 && dbName != "" {
			args = append(args, dbName)
		}

		// no need to persist preference in this function because it will be done
		// in foundCmd before return or when os.Interrupt signal is caught there
		return foundCmd.RunE(foundCmd, args)
	},
}
