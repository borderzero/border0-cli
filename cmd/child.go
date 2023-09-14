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
	"runtime"
	"syscall"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type sftpChildArgs struct {
	uid    int
	gid    int
	user   string
	groups []int
}

type stdioWrapper struct {
	io.Reader
	io.Writer
}

func (sw *stdioWrapper) Close() error { return nil }

var sftpArgs sftpChildArgs

var childCmd = &cobra.Command{
	Use:   "child",
	Short: "Start a child process",
}

var childSftpCmd = &cobra.Command{
	Use:   "sftp",
	Short: "Start a sftp child process",
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := logger.Logger
		logger.Info("Starting sftp child process", zap.String("user", sftpArgs.user), zap.Int("uid", sftpArgs.uid), zap.Int("gid", sftpArgs.gid))

		euid := os.Geteuid()
		egid := os.Getegid()

		if euid != 0 && sftpArgs.uid != euid {
			return fmt.Errorf("command must be run as root or with sudo")
		}

		if runtime.GOOS == "darwin" && len(sftpArgs.groups) > 16 {
			sftpArgs.groups = sftpArgs.groups[:16]
		}

		if err := syscall.Setgroups(sftpArgs.groups); err != nil {
			return fmt.Errorf("failed to set groups: %w", err)
		}

		if egid != sftpArgs.gid {
			if err := syscall.Setgid(sftpArgs.gid); err != nil {
				return fmt.Errorf("failed to set gid: %w", err)
			}
		}

		if euid != sftpArgs.uid {
			if err := syscall.Setuid(sftpArgs.uid); err != nil {
				return fmt.Errorf("failed to set uid: %w", err)
			}
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

	childSftpCmd.Flags().IntVar(&sftpArgs.uid, "uid", 0, "uid")
	childSftpCmd.Flags().IntVar(&sftpArgs.gid, "gid", 0, "gid")
	childSftpCmd.Flags().StringVar(&sftpArgs.user, "user", "", "user")
	childSftpCmd.Flags().IntSliceVar(&sftpArgs.groups, "group", []int{}, "groups")

	childSftpCmd.MarkFlagRequired("uid")
	childSftpCmd.MarkFlagRequired("gid")
	childSftpCmd.MarkFlagRequired("user")
}
