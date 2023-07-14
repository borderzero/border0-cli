/*
Copyright © 2020 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"fmt"
	"log"

	"github.com/AlecAivazis/survey/v2"

	"github.com/borderzero/border0-cli/client/preference"
	"github.com/borderzero/border0-cli/cmd/client/db"
	clientTls "github.com/borderzero/border0-cli/cmd/client/tls"
	"github.com/borderzero/border0-cli/cmd/logger"

	"github.com/borderzero/border0-cli/cmd/client/hosts"
	"github.com/borderzero/border0-cli/cmd/client/ssh"
	"github.com/borderzero/border0-cli/internal/client"
	"github.com/spf13/cobra"
)

// clientCmd represents the client command
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Client commands",
}

var clientCertCmd = &cobra.Command{
	Use:   "cert",
	Short: "Client certificates",
}

var clientCertFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch Client certificate",
	RunE: func(cmd *cobra.Command, args []string) error {
		if crtPath, keyPath, ok := client.IsClientCertValid(); !ok {
			crtPath, keyPath, err := client.FetchCertAndReturnPaths(logger.Logger, hostname)
			if err != nil {
				return err
			}
			fmt.Println("Client certificate file:", crtPath, "and", keyPath)

		} else {
			fmt.Println("Client certificate file:", crtPath, "and", keyPath)
		}
		return nil
	},
}

// clientLoginCmd represents the client login DNS command
var clientLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login and get API token so service can authenticate",
	Run: func(cmd *cobra.Command, args []string) {
		if orgID == "" {
			pref, err := preference.Read()
			if err != nil {
				fmt.Println("WARNING: could not read preference file:", err)
			}
			subdomains := pref.RecentlyUsedOrgs(5).Subdomains()

			if len(subdomains) == 1 {
				orgID = subdomains[0]
				fmt.Println("Logging into", orgID, "org...")
			} else if len(subdomains) > 0 {
				if err := survey.AskOne(&survey.Select{
					Message: "choose an org:",
					Options: subdomains,
				}, &orgID, survey.WithValidator(survey.Required)); err != nil {
					fmt.Println("failed to read input:", err)
					return
				}
			}

			if orgID == "" {
				if err = survey.AskOne(&survey.Input{
					Message: "enter an org subdomain:",
				}, &orgID, survey.WithValidator(survey.Required)); err != nil {
					fmt.Println("failed to read input:", err)
					return
				}
			}
		}

		_, claims, err := client.Login(orgID)
		if err != nil {
			log.Fatal(err)
		}

		// read preference file and write logged in org info back to preference file
		id, subdomain := fmt.Sprint(claims["org_id"]), fmt.Sprint(claims["org_subdomain"])
		if err := preference.CreateOrUpdate(id, subdomain); err != nil {
			fmt.Println(err)
		}

		fmt.Println("Login successful")
	},
}

// clientLoginCmd represents the client login DNS command
var clientLoginStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check login status, see if token is still valid",
	Run: func(cmd *cobra.Command, args []string) {
		valid, _, email, err := client.IsExistingClientTokenValid("")
		if !valid {
			fmt.Println(err)
			fmt.Println("Please login again: border0 client login")
		} else {
			fmt.Println("Token Valid, logged in as " + email)
		}
	},
}

func init() {
	rootCmd.AddCommand(clientCmd)

	clientCmd.AddCommand(clientCertCmd)
	clientCertCmd.AddCommand(clientCertFetchCmd)
	clientCertFetchCmd.Flags().StringVarP(&hostname, "host", "", "", "The border0 target host")
	clientCertFetchCmd.MarkFlagRequired("host")

	clientCmd.AddCommand(clientLoginCmd)
	clientLoginCmd.Flags().StringVarP(&orgID, "org", "", "", "The border0 organization domain name (without .border0.io)")
	clientLoginCmd.Flags().IntVarP(&port, "port", "p", 0, "Port number")

	clientLoginCmd.AddCommand(clientLoginStatusCmd)

	db.AddCommandsTo(clientCmd)
	hosts.AddCommandsTo(clientCmd)
	ssh.AddCommandsTo(clientCmd)
	clientTls.AddCommandsTo(clientCmd)
}
