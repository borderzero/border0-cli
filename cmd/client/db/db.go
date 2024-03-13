package db

import (
	"fmt"
	"runtime"

	"github.com/AlecAivazis/survey/v2"
	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/client/sqlclientproxy"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var (
	hostname string
	local    bool
	port     int
	wsProxy  string
)

var (
	wsProxyToOriginHeader = map[string]string{
		"wss://ws.border0.com/ws":         "https://client.border0.com",
		"wss://ws.staging.border0.com/ws": "https://client.staging.border0.com",
	}
)

func AddCommandsTo(client *cobra.Command) {
	addOneCommandTo(dbCmd, client)
	addOneCommandTo(mysqlCmd, client)
	addOneCommandTo(mycliCmd, client)
	addOneCommandTo(mysqlWorkbenchCmd, client)
	addOneCommandTo(dbeaverCmd, client)
	addOneCommandTo(psqlCmd, client)
	addOneCommandTo(pgcliCmd, client)
	addOneCommandTo(dataGripCmd, client)
	addOneCommandTo(sqlcmdCmd, client)
	addOneCommandTo(ssmsCmd, client)

	dbCmd.Flags().BoolVarP(&local, "local", "l", false, "start a local listener")
	dbCmd.Flags().IntVarP(&port, "port", "p", 0, "local listener port")
}

func addOneCommandTo(cmdToAdd, cmdAddedTo *cobra.Command) {
	cmdToAdd.Flags().StringVarP(&hostname, "host", "", "", "Socket target host")
	cmdToAdd.Flags().StringVarP(&wsProxy, "wsproxy", "w", "", "websocket proxy")
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

		if local {
			proxy, err := sqlclientproxy.NewSqlClientProxy(logger.Logger, port, pickedHost, wsProxy)
			if err != nil {
				return fmt.Errorf("failed to start local listener: %w", err)
			}

			return proxy.Listen()
		}

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

		var (
			dbClient            string
			dbClients           = []string{"local listener"}
			dbClientsMySQL      = []string{"mysql", "mysqlworkbench", "mycli", "dbeaver", "datagrip"}
			dbClientsPostgreSQL = []string{"psql", "pgcli", "datagrip"}
			dbClientsMssql      = []string{"sqlcmd", "dbeaver", "datagrip"}
		)

		switch pickedHost.DatabaseType {
		case "mysql":
			dbClients = append(dbClients, dbClientsMySQL...)
		case "mssql":
			dbClients = append(dbClients, dbClientsMssql...)
			if runtime.GOOS == "windows" {
				dbClients = append(dbClients, "ssms (SQL Server Management Studio)")
			}
		case "postgres":
			dbClients = append(dbClients, dbClientsPostgreSQL...)
		default:
			dbClients = append(dbClients, dbClientsMySQL...)
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

		if dbClient == "local listener" {
			proxy, err := sqlclientproxy.NewSqlClientProxy(logger.Logger, port, pickedHost, wsProxy)
			if err != nil {
				return fmt.Errorf("failed to start local listener: %w", err)
			}

			return proxy.Listen()
		}

		dbName, err = client.EnterDBName(dbName, suggestedDBName)
		if err != nil {
			return err
		}

		if dbClient == "ssms (SQL Server Management Studio)" {
			dbClient = "ssms"
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
