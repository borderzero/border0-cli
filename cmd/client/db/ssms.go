package db

import (
	"fmt"
	"os"
	"runtime"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/borderzero/border0-cli/internal/enum"
	"github.com/spf13/cobra"
)

var ssmsPossiblePaths = []string{
	"c:\\Program Files (x86)\\Microsoft SQL Server Management Studio 19\\Common7\\IDE\\Ssms.exe",
	"c:\\Program Files (x86)\\Microsoft SQL Server Management Studio 18\\Common7\\IDE\\Ssms.exe",
	"c:\\Program Files\\Microsoft SQL Server Management Studio 19\\Common7\\IDE\\Ssms.exe",
	"c:\\Program Files\\Microsoft SQL Server Management Studio 18\\Common7\\IDE\\Ssms.exe",
	"c:\\Program Files (x86)\\Microsoft SQL Server\\140\\Tools\\Binn\\ManagementStudio\\Ssms.exe", // SQL Server 2017
	"c:\\Program Files\\Microsoft SQL Server\\140\\Tools\\Binn\\ManagementStudio\\Ssms.exe",       // SQL Server 2017
	"c:\\Program Files (x86)\\Microsoft SQL Server\\130\\Tools\\Binn\\ManagementStudio\\Ssms.exe", // SQL Server 2016
	"c:\\Program Files\\Microsoft SQL Server\\130\\Tools\\Binn\\ManagementStudio\\Ssms.exe",       // SQL Server 2016
	"d:\\Program Files (x86)\\Microsoft SQL Server Management Studio 19\\Common7\\IDE\\Ssms.exe",
	"d:\\Program Files (x86)\\Microsoft SQL Server Management Studio 18\\Common7\\IDE\\Ssms.exe",
	"d:\\Program Files\\Microsoft SQL Server Management Studio 19\\Common7\\IDE\\Ssms.exe",
	"d:\\Program Files\\Microsoft SQL Server Management Studio 18\\Common7\\IDE\\Ssms.exe",
	"d:\\Program Files (x86)\\Microsoft SQL Server\\140\\Tools\\Binn\\ManagementStudio\\Ssms.exe", // SQL Server 2017
	"d:\\Program Files\\Microsoft SQL Server\\140\\Tools\\Binn\\ManagementStudio\\Ssms.exe",       // SQL Server 2017
	"d:\\Program Files (x86)\\Microsoft SQL Server\\130\\Tools\\Binn\\ManagementStudio\\Ssms.exe", // SQL Server 2016
	"d:\\Program Files\\Microsoft SQL Server\\130\\Tools\\Binn\\ManagementStudio\\Ssms.exe",       // SQL Server 2016

}

var ssmsCmd = &cobra.Command{
	Use:   "db:ssms",
	Short: "Connect to a database socket with ssms (SQL Server Management Studio)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// check if ssms is installed
		if runtime.GOOS != "windows" {
			return fmt.Errorf("ssms is only available on Windows")
		}

		// check if ssms is installed
		var ssmsPath string
		for _, path := range ssmsPossiblePaths {
			if _, err := os.Stat(path); err == nil {
				ssmsPath = path
				break
			}
		}

		if ssmsPath == "" {
			return fmt.Errorf("unable to find ssms executable")
		}

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
		socketPref.DatabaseClient = "sqlcmd"
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
				return fmt.Errorf("could not start listener: %w", err)
			}

			hostname = "127.0.0.1"
		}

		err = client.ExecCommand(ssmsPath, []string{
			"-S", fmt.Sprintf("tcp:%s,%d", hostname, info.Port),
			"-d", dbName,
			"-U", "border0",
		}...)

		return err
	},
}
