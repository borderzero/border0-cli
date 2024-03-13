package db

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/client/datagrip"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/borderzero/border0-cli/internal/util"
	"github.com/spf13/cobra"
)

var dataGripCmd = &cobra.Command{
	Use:   "db:datagrip",
	Short: "Connect to a database socket with DataGrip",
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
		socketPref.DatabaseClient = "datagrip"
		pref.SetSocket(socketPref)

		info, err := client.GetResourceInfo(logger.Logger, hostname)
		if err != nil {
			return err
		}

		connectionName := hostname

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled || wsProxy != "" {
			info.Port, err = client.StartConnectorAuthListener(hostname, info.Port, info.SetupTLSCertificate(), info.CaCertificate, 0, info.ConnectorAuthenticationEnabled, info.EndToEndEncryptionEnabled, wsProxy)
			if err != nil {
				return fmt.Errorf("could not start listener: %w", err)
			}

			hostname = "localhost"
		}

		certChainPath, err := client.DownloadCertificateChain(hostname)
		if err != nil {
			return err
		}

		xmlDoc, err := datagrip.DataSourcesXML(&datagrip.Config{
			Type:        pickedHost.DatabaseType,
			Name:        connectionName,
			Host:        hostname,
			Port:        info.Port,
			Database:    dbName,
			CAPath:      certChainPath,
			SSLCertPath: info.CertificatePath,
			SSLKeyPath:  info.PrivateKeyPath,
		})
		if err != nil {
			return err
		}
		home, err := util.GetUserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home dir: %w", err)
		}
		// create dir if not exists
		configPath := filepath.Join(home, ".border0", fmt.Sprintf("datagrip_%s", connectionName))
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if err := os.Mkdir(configPath, 0700); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", configPath, err)
			}
		}
		dotIdeaPath := filepath.Join(configPath, ".idea")
		if _, err := os.Stat(dotIdeaPath); os.IsNotExist(err) {
			if err := os.Mkdir(dotIdeaPath, 0700); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dotIdeaPath, err)
			}
		}
		xmlPath := filepath.Join(dotIdeaPath, "dataSources.xml")
		if err = ioutil.WriteFile(xmlPath, []byte(xmlDoc), 0600); err != nil {
			return fmt.Errorf("failed writing DataGrip dataSources.xml file: %w", err)
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

		fmt.Println("Starting up DataGrip...")
		switch runtime.GOOS {
		case "darwin":
			err = client.ExecCommand("open", "-a", "datagrip", "--args", configPath)
		case "windows":
			command := []string{"cmd", "/C", "start", "", "datagrip.exe", configPath}
			found := client.FindWindowsExecutable(`C:\Program Files\JetBrains`, "DataGrip", filepath.Join("bin", "datagrip64.exe"))
			if len(found) > 0 {
				command = []string{"cmd", "/C", "start", "", found, configPath}
			}
			if err = client.ExecCommand(command[0], command[1:]...); err != nil {
				return errors.New(`failed to start DataGrip. Please make sure DataGrip executable (datagrip64.exe) is either in system's PATH, or installed in C:\Program Files\.`)
			}
		default:
			// linux
			err = client.ExecCommand("datagrip", configPath)
		}

		if info.ConnectorAuthenticationEnabled || info.EndToEndEncryptionEnabled {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
			<-ch
		}

		return err
	},
}
