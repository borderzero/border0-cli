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
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/borderzero/border0-cli/internal/ssh"
	"github.com/spf13/cobra"
)

// connectCmd represents the connect command
var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Quickly connect, wrapper around sockets and tunnels",
	Run: func(cmd *cobra.Command, args []string) {
		if name == "" {
			log.Fatalf("error: empty name not allowed")
		}

		socketType := strings.ToLower(socketType)
		if socketType != "http" && socketType != "https" && socketType != "tls" && socketType != "ssh" && socketType != "database" {
			log.Fatalf("error: --type should be either http, https, database, ssh or tls")
		}

		upstreamType := strings.ToLower(upstream_type)
		if socketType == "http" || socketType == "https" {
			if upstreamType != "http" && upstreamType != "https" && upstreamType != "" {
				log.Fatalf("error: --upstream_type should be either http, https")
			}
		}

		if socketType == "database" {
			if upstreamType != "mysql" && upstreamType != "postgres" && upstreamType != "" {
				log.Fatalf("error: --upstream_type should be  mysql or postgres, defaults to mysql")
			}
		}

		socketToCreate := &models.Socket{
			Name:                           name,
			Description:                    description,
			SocketType:                     socketType,
			UpstreamUsername:               &upstream_username,
			UpstreamPassword:               &upstream_password,
			UpstreamHttpHostname:           upstream_http_hostname,
			UpstreamType:                   upstreamType,
			CloudAuthEnabled:               true,
			ConnectorAuthenticationEnabled: connectorAuthEnabled,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		border0API := api.NewAPI(api.WithVersion(version))
		socketFromAPI, err := border0API.CreateSocket(ctx, socketToCreate)
		if err != nil {
			log.Fatalf("failed to create socket: %s", err)
		}

		policies, err := border0API.GetPoliciesBySocketID(socketFromAPI.SocketID)
		if err != nil {
			log.Fatalf("failed to get policies: %s", err)
		}

		fmt.Print(print_socket(*socketFromAPI, policies))

		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-ch
			fmt.Println("cleaning up...")
			if err := border0API.DeleteSocket(ctx, socketFromAPI.SocketID); err != nil {
				log.Fatalf("failed to cleunup socket: %s", err)
			}
			os.Exit(0)
		}()

		SetRlimit()

		if socketType != "ssh" && localssh {
			localssh = false
		}

		if socketType != "http" && httpserver {
			httpserver = false
		}

		socket, err := border0.NewSocket(context.Background(), border0API, socketFromAPI.SocketID)
		if err != nil {
			log.Fatalf("error: %v", err)
		}

		socket.WithVersion(version)

		if proxyHost != "" {
			if err := socket.WithProxy(proxyHost); err != nil {
				log.Fatalf("error: %v", err)
			}
		}

		border0API.StartRefreshAccessTokenJob(ctx)

		l, err := socket.Listen()
		if err != nil {
			log.Fatalf("error: %v", err)
		}

		defer l.Close()

		switch {
		case httpserver:
			http.StartLocalHTTPServer(httpserver_dir, l)
		case localssh:
			sshServer, err := ssh.NewServer(logger.Logger, socket.Organization.Certificates["ssh_public_key"])
			if err != nil {
				log.Fatalf("error: %v", err)
			}
			sshServer.Serve(l)
		default:
			if port < 1 {
				log.Fatalf("error: port not specified")
			}
			border0.Serve(logger.Logger, l, hostname, port)
		}

		fmt.Println("cleaning up...")
		if err := border0API.DeleteSocket(context.Background(), socketFromAPI.SocketID); err != nil {
			log.Fatalf("failed to cleunup socket: %s", err)
		}
	},
}

func init() {
	connectCmd.Flags().IntVarP(&port, "port", "p", 0, "Port")
	connectCmd.Flags().StringVarP(&hostname, "host", "", "127.0.0.1", "Target host: Control where inbound traffic goes. Default localhost")
	connectCmd.Flags().StringVarP(&name, "name", "n", "", "Service name")
	connectCmd.Flags().StringVarP(&description, "description", "r", "", "Service description")
	connectCmd.Flags().StringVarP(&password, "password", "", "", "Password, required when protected set to true")
	connectCmd.Flags().StringVarP(&socketType, "type", "t", "http", "Socket type: http, https, ssh, tls, database")
	connectCmd.Flags().StringVarP(&upstream_username, "upstream_username", "j", "", "Upstream username used to connect to upstream database")
	connectCmd.Flags().StringVarP(&upstream_password, "upstream_password", "k", "", "Upstream password used to connect to upstream database")
	connectCmd.Flags().StringVarP(&upstream_http_hostname, "upstream_http_hostname", "", "", "Upstream http hostname")
	connectCmd.Flags().StringVarP(&upstream_type, "upstream_type", "", "", "Upstream type: Upstream type: http, https for http sockets or mysql, postgres for database sockets")
	connectCmd.Flags().StringVarP(&proxyHost, "proxy", "", "", "Proxy host used for connection to border0")
	connectCmd.Flags().BoolVarP(&localssh, "localssh", "", false, "Start a local SSH server to accept SSH sessions on this host")
	connectCmd.Flags().BoolVarP(&localssh, "sshserver", "l", false, "Start a local SSH server to accept SSH sessions on this host")
	connectCmd.Flags().BoolVarP(&httpserver, "httpserver", "", false, "Start a local http server to accept http connections on this host")
	connectCmd.Flags().StringVarP(&httpserver_dir, "httpserver_dir", "", "", "Directory to serve http connections on this host")
	connectCmd.Flags().MarkDeprecated("localssh", "use --sshserver instead")
	connectCmd.Flags().MarkDeprecated("allowed_email_domains", "use policies instead")
	connectCmd.Flags().MarkDeprecated("allowed_email_addresses", "use policies instead")
	connectCmd.Deprecated = "use 'socket connect' instead"

	connectCmd.MarkFlagRequired("name")

	rootCmd.AddCommand(connectCmd)
}
