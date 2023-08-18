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
	"os"
	"strconv"
	"strings"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/jedib0t/go-pretty/table"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
)

const domainSuffix = "border0.io"

var (
	version                 string
	date                    string
	email                   string
	mfaCode                 string
	name                    string
	description             string
	socketType              string
	password                string
	port                    int
	hostname                string
	orgID                   string
	socketID                string
	tunnelID                string
	policyName              string
	policyDescription       string
	policyFile              string
	identityFile            string //deprecated
	cloudauth_addresses     string
	cloudauth_domains       string
	proxyHost               string
	upstream_username       string
	upstream_password       string
	upstream_http_hostname  string
	upstream_type           string
	httpserver              bool
	httpserver_dir          string
	localssh                bool
	orgName                 string
	sso                     string
	connectorConfig         string
	perPage                 int64
	page                    int64
	orgwide                 bool
	runcommand              string
	connectorAuthEnabled    bool
	orgCustomDomain         string
	upstream_cert_file      string
	upstream_key_file       string
	upstream_ca_file        string
	cloudSqlCredentialsFile string
	cloudSqlInstance        string
	cloudSqlIAM             bool
	cloudSqlConnector       bool
	rdsIAM                  bool
	aws                     bool
	awsRegion               string
	awsProfile              string
	upstream_tls            bool
	upstream_identify_file  string
	awsECSCluster           string
	awsECSServices          []string
	awsECSTasks             []string
	awsECSContainers        []string
	disableBrowser          bool
	awsEc2InstanceId        string
	awsEc2InstanceConnect   bool
	vpnSubnet               string   // used in the socket connect vpn command
	routes                  []string // used in the socket connect vpn command
	allowedProxyHosts       []string // used in the socket connect proxy command

)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "border0",
	Short:   "border0 command line interface (CLI)",
	Version: version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	defer logger.Logger.Sync()

	cc.Init(&cc.Config{
		RootCmd:  rootCmd,
		Headings: cc.HiCyan + cc.Underline, NoExtraNewlines: true,
		Commands: cc.HiYellow,
		Example:  cc.Italic,
		ExecName: cc.Bold,
		Flags:    cc.Bold,
	})
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate(fmt.Sprintf("border0:\nversion %s\ndate: %s\n", version, date))
}

func splitLongLines(b string, maxLength int) string {
	s := ""
	for {
		if len(b) > maxLength {
			s = s + b[0:maxLength] + "\n"
			b = b[maxLength:]
		} else {
			s = s + b
			break
		}
	}

	return s
}

func print_socket(s models.Socket, policies []models.Policy) string {

	socket_output := ""
	t := table.NewWriter()
	t.AppendHeader(table.Row{"Socket ID", "Name", "DNS Name", "Port(s)", "Type", "Description"})

	portsStr := ""
	for _, p := range s.SocketTcpPorts {
		i := strconv.Itoa(p)
		if portsStr == "" {
			portsStr = i
		} else {
			portsStr = portsStr + ", " + i
		}
	}

	t.AppendRow(table.Row{s.SocketID, s.Name, s.Dnsname, portsStr, s.SocketType, s.Description})
	t.SetStyle(table.StyleLight)
	socket_output = socket_output + fmt.Sprintf("%s\n", t.Render())

	// Check if we still do cloud auth.
	printCloudAuth := false
	if len(s.AllowedEmailAddresses) > 0 {
		printCloudAuth = true
	}
	if len(s.AllowedEmailDomains) > 0 {
		printCloudAuth = true
	}
	if printCloudAuth {
		tc := table.NewWriter()
		tc.AppendHeader(table.Row{"Allowed email addresses", "Allowed email domains"})
		tc.AppendRow(table.Row{strings.Join(s.AllowedEmailAddresses, "\n"), strings.Join(s.AllowedEmailDomains, "\n")})
		tc.SetStyle(table.StyleLight)
		socket_output = socket_output + fmt.Sprintf("\nCloud Authentication, login details:\n%s\n", tc.Render())
	}

	if s.SocketType == "http" || s.SocketType == "https" {
		th := table.NewWriter()
		th.AppendHeader(table.Row{"Upstream Type", "Upstream Hostname"})
		th.AppendRow(table.Row{s.UpstreamType, pointer.ValueOrZero(s.UpstreamHttpHostname)})
		th.SetStyle(table.StyleLight)
		if s.UpstreamType != "" || s.UpstreamHttpHostname != nil && *s.UpstreamHttpHostname != "" {
			socket_output = socket_output + fmt.Sprintf("\nHTTP Options:\n%s\n", th.Render())
		}
	}

	// Merge the the policy lists together
	//s.Policies = append(s.Policies, policies...)
	policies = append(policies, s.Policies...)

	tp := table.NewWriter()

	tp.AppendHeader(table.Row{"Policy Name", "Policy Description", "Organization Wide"})
	for _, p := range policies {
		orgWide := "No"

		if p.OrgWide {
			orgWide = "Yes"
		}
		tp.AppendRow(table.Row{p.Name, p.Description, orgWide})
	}
	tp.SetStyle(table.StyleLight)
	socket_output = socket_output + fmt.Sprintf("\nPolicies:\n%s\n", tp.Render())

	if len(policies) == 0 {
		socket_output = socket_output + "⚠️ Warning: No policies\n"
		socket_output = socket_output + "No policies are attached to this socket. This means that no one will be able to connect to this socket.\n"
		socket_output = socket_output + "To resolve this, attach a Policy, or create an Organization-wide Policy.\n"
	}

	return socket_output
}
