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
	"fmt"
	"io"
	"os"

	"github.com/borderzero/border0-cli/internal/process"
	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
)

type stdioWrapper struct {
	io.Reader
	io.Writer
}

func (sw *stdioWrapper) Close() error { return nil }

var sftpArgs process.Parameters

var childCmd = &cobra.Command{
	Use:    "child",
	Short:  "Start a child process",
	Hidden: true,
}

var childSftpCmd = &cobra.Command{
	Use:   "sftp",
	Short: "Start a sftp child process",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := process.SetupUserAndGroups(&sftpArgs); err != nil {
			return fmt.Errorf("failed to set up process user and groups: %v", err)
		}

		server, err := sftp.NewServer(
			&stdioWrapper{os.Stdin, os.Stdout},
		)
		if err != nil {
			return fmt.Errorf("sftp server init error: %s", err)
		}

		if err := server.Serve(); err == io.EOF {
			server.Close()
		} else if err != nil {
			return fmt.Errorf("sftp server completed with error: %s", err)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(childCmd)
	childCmd.AddCommand(childSftpCmd)

	childSftpCmd.Flags().IntVar(&sftpArgs.UID, "uid", 0, "uid")
	childSftpCmd.Flags().IntVar(&sftpArgs.GID, "gid", 0, "gid")
	childSftpCmd.Flags().StringVar(&sftpArgs.User, "user", "", "user")
	childSftpCmd.Flags().IntSliceVar(&sftpArgs.Groups, "group", []int{}, "groups")

	childSftpCmd.MarkFlagRequired("uid")
	childSftpCmd.MarkFlagRequired("gid")
	childSftpCmd.MarkFlagRequired("user")
}
