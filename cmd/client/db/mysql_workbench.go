package db

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/client/mysqlworkbench"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/spf13/cobra"
)

var mysqlWorkbenchCmd = &cobra.Command{
	Use:   "db:mysqlworkbench",
	Short: "Connect to a database socket with MySQL Workbench",
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
		socketPref.DatabaseClient = "mysqlworkbench"
		pref.SetSocket(socketPref)

		info, err := client.GetResourceInfo(logger.Logger, hostname)
		if err != nil {
			return err
		}

		connectionName := hostname

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
			info.Port, err = client.StartConnectorAuthListener(hostname, info.Port, info.SetupTLSCertificate(), info.CaCertificate, 0, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled)
			if err != nil {
				fmt.Println("ERROR: could not setup listener:", err)
				return err
			}

			hostname = "localhost"
		}

		// for more info about mysql workbench command line options and config files, see:
		// https://dev.mysql.com/doc/workbench/en/wb-command-line-options.html
		// https://dev.mysql.com/doc/workbench/en/wb-configuring-files.html
		xmlDoc, err := mysqlworkbench.ConnectionsXML(connectionName, hostname, info.Port, info.CertificatePath, info.PrivateKeyPath, dbName)
		if err != nil {
			return err
		}
		home, err := util.GetUserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home dir : %w", err)
		}
		// create dir if not exists
		configPath := filepath.Join(home, ".border0", "mysqlworkbench")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if err := os.Mkdir(configPath, 0700); err != nil {
				return fmt.Errorf("failed to create directory %s : %w", configPath, err)
			}
		}
		xmlPath := filepath.Join(configPath, "connections.xml")
		if err = os.WriteFile(xmlPath, []byte(xmlDoc), 0600); err != nil {
			return fmt.Errorf("failed writing MySQL Workbench connections.xml file: %w", err)
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

		fmt.Println("Starting up MySQL Workbench...")
		switch runtime.GOOS {
		case "darwin":
			err = client.ExecCommand("open", "-a", "MySQLWorkbench", "--args", "--configdir", configPath, "--query", connectionName)
		case "windows":
			command := []string{"cmd", "/C", "start", "", "MySQLWorkbench.exe", "--configdir", configPath, "--query", connectionName}
			found := client.FindWindowsExecutable(`C:\Program Files\MySQL`, "MySQL Workbench", "MySQLWorkbench.exe")
			if len(found) > 0 {
				command = []string{"cmd", "/C", "start", "", found, "--configdir", configPath, "--query", connectionName}
			}
			if err = client.ExecCommand(command[0], command[1:]...); err != nil {
				return fmt.Errorf(`failed to start MySQL Workbench, please make sure the executable is either in system's PATH, or installed in C:\Program Files\`)
			}
		default:
			err = client.ExecCommand("mysql-workbench", "--configdir", configPath, "--query", connectionName)
		}

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
			<-ch
		}

		return err
	},
}
