/*
Copyright © 2020 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARR    ANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/borderzero/border0-cli/internal"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "check version",
}

var checkLatestVersionCmd = &cobra.Command{
	Use:   "check",
	Short: "Check to see if you're running the latest version",
	Run: func(cmd *cobra.Command, args []string) {
		latest_version, err := http.GetLatestVersion()
		if err != nil {
			log.Fatalf("error while checking for latest version: %v", err)
		}
		if latest_version != internal.Version {
			binary_path := os.Args[0]
			fmt.Printf("You're running version %s\n\n", internal.Version)
			fmt.Printf("There is a newer version available (%s)!\n", latest_version)
			fmt.Printf("Please upgrade:\n%s version upgrade\n", binary_path)
		} else {
			fmt.Printf("You are up to date!\n")
			fmt.Printf("You're running version %s\n", internal.Version)
		}
	},
}
var upgradeVersionCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "upgrade the latest version",
	Run: func(cmd *cobra.Command, args []string) {
		binary_path, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		latest_version, err := http.GetLatestVersion()
		if err != nil {
			log.Fatalf("error while checking for latest version: %v", err)
		}
		if latest_version != internal.Version {
			fmt.Printf("Upgrading %s to version %s\n", binary_path, latest_version)
		} else {
			fmt.Printf("You are up to date already :)\n")
			return
		}

		checksum, latest, err := http.GetLatestBinary(runtime.GOOS, runtime.GOARCH)
		if latest == nil {
			log.Fatalf("Error while downloading latest version %v", err)
		}
		local_checksum := fmt.Sprintf("%x", sha256.Sum256(latest))
		if checksum != local_checksum {
			log.Fatalf(`Checksum error: Checksum of downloaded binary, doesn't match published checksum
            published checksum: %s
            downloaded binary checksum: %s`, checksum, local_checksum)
		}

		tmpfile, err := os.CreateTemp("", "border0-"+latest_version)
		if err != nil {
			log.Fatal("cant create temp file", err)
		}
		if err := tmpfile.Close(); err != nil {
			log.Fatal("cant close file", err)
		}

		defer os.Remove(tmpfile.Name())

		err = os.WriteFile(tmpfile.Name(), latest, 0644)
		if err != nil {
			log.Fatalf("Error while writing new file: %v", err)
		}
		tmpfile.Close()
		if err := os.Chmod(tmpfile.Name(), 0755); err != nil {
			log.Fatal("cant close tmp file", err)
		}

		// Get the current permissions of the binary
		info, err := os.Stat(binary_path)
		if err != nil {
			log.Fatal("cant get permissions", err)
		}
		originalPermissions := info.Mode()

		// Define a backup file path
		backupPath := binary_path + ".bak"

		// 1. Move the running binary to the backup file
		origFile, err := os.ReadFile(binary_path)
		if err != nil {
			log.Fatal("cant rename binary to backup", err)
		}
		err = os.WriteFile(backupPath, origFile, 0644)

		// Copy the content from the temporary file to the binary path
		// Can't just do a straight up rename because it could be on a different filesystem partition
		err = copyFile(tmpfile.Name(), binary_path)
		if err != nil {
			log.Fatal("copy error", err)
		}

		// Remove the temporary file
		err = os.Remove(tmpfile.Name())
		if err != nil {
			log.Fatal("can't remove temp file", err)
		}

		// After copying the new binary, set its permissions to the original permissions
		err = os.Chmod(binary_path, originalPermissions)
		if err != nil {
			log.Printf("error restoring permissions on the new binary: %v\n", err)
			// Optionally, revert to the backup file
			revertErr := os.Rename(backupPath, binary_path)
			if revertErr != nil {
				log.Printf("Error reverting to the backup binary: %v\n", revertErr)
			}
			log.Fatal("Reverted to the old version of the border0 cli due to an error while restoring permissions on the new binary")
		}

		// Execute the new binary just to make sure it's working
		command := exec.Command(binary_path)
		output, err := command.CombinedOutput()
		if err != nil {
			log.Printf("Error executing the new version border0: %v\nOutput: %s\n", err, output)
			// Optionally, revert to the backup file
			revertErr := os.Rename(backupPath, binary_path)
			if revertErr != nil {
				log.Printf("Error reverting to the backup binary: %v\n", revertErr)
			}
			log.Fatal("Reverted to the old version of the border0 cli due to an error while executing the new binary")
		}

		// sometimes windows will lock the file and we can't move it.
		// Possibly due to virus scanner or something else
		// So we'll try 3 times, before giving up
		deleteOk := false
		for i := 0; i < 3; i++ {
			err = os.Remove(backupPath)
			if err == nil {
				deleteOk = true
				break
			} else {
				fmt.Printf("Warning: Error removing backup file: %v\n", err)
				time.Sleep(4 * time.Second)
			}
		}
		if !deleteOk {
			if runtime.GOOS == "windows" {
				// lets use os.exe to delete the file
				cmd := exec.Command("cmd", "/C", "del", backupPath)
				err := cmd.Run()
				if err != nil {
					log.Printf("Error removing backup file: %v\n", err)
				}
			}

			log.Printf("Warning: Error removing backup file: %v\n", err)
		}

		fmt.Printf("Upgrade completed\n")
	},
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}
	dstFile.Sync()
	dstFile.Close()

}

func init() {
	versionCmd.AddCommand(checkLatestVersionCmd)
	versionCmd.AddCommand(upgradeVersionCmd)
	rootCmd.AddCommand(versionCmd)
}
