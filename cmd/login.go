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
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"os"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/cenkalti/backoff/v4"
	jwt "github.com/golang-jwt/jwt"
	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to border0 and get a token",
	Run: func(cmd *cobra.Command, args []string) {

		// Do version check
		latest_version, err := http.GetLatestVersion()
		if err != nil {
			log.Fatalf("error while checking for latest version: %v", err)
		}
		if latest_version != version {
			binary_path := os.Args[0]
			fmt.Printf("New version available. Please upgrade:\n%s version upgrade\n\n", binary_path)
		}
		// end version check

		if email == "" && password == "" && sso != "" {
			sessionToken, err := http.CreateDeviceAuthorization()
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			sessionJWT, _ := jwt.Parse(sessionToken, nil)
			if sessionJWT == nil {
				log.Fatalf("We couldn't log you in, your session is expired or you are not authorized to perform this action")
				return
			}

			claims := sessionJWT.Claims.(jwt.MapClaims)
			deviceIdentifier := fmt.Sprint(claims["identifier"])

			url := fmt.Sprintf("%s/login?device_identifier=%v", http.WebUrl(), url.QueryEscape(deviceIdentifier))
			fmt.Printf("Please navigate to the URL below in order to complete the login process:\n%s\n", url)

			// Try opening the system's browser automatically. The error is ignored because the desired behavior of the
			// handler is the same regardless of whether opening the browser fails or succeeds -- we still print the URL.
			// This is desirable because in the event opening the browser succeeds, the customer may still accidentally
			// close the new tab / browser session, or may want to authenticate in a different browser / session. In the
			// event that opening the browser fails, the customer may still complete authenticating by navigating to the
			// URL in a different device.

			/// check if the disableBrowser flag is set
			if !disableBrowser {
				_ = open.Run(url)
			}

			// Polling for token
			i := 1
			for {
				retriesThreeTimesEveryTwoSeconds := backoff.WithMaxRetries(backoff.NewConstantBackOff(2*time.Second), 3)

				var token *models.SessionTokenForm
				var err error

				err = backoff.Retry(func() error {
					token, err = http.GetDeviceAuthorization(sessionToken)
					return err
				}, retriesThreeTimesEveryTwoSeconds)

				if err != nil {
					if errors.Is(err, http.ErrUnauthorized) {
						log.Fatalf("We couldn't log you in, your session is expired or you are not authorized to perform this action: %v", err)
					}

					log.Fatalf("We couldn't log you in, make sure that you are properly logged in using the link above: %v", err)
				}

				if token != nil && token.Token != "" && token.State != "not_authorized" {
					fmt.Println("Login successful")
					if err := http.SaveTokenInDisk(token.Token); err != nil {
						log.Fatalf("failed to save token: %s", err)
					}
					return
				}

				if i < 10 {
					time.Sleep(1 * time.Second)
				} else if i < 20 {
					time.Sleep(2 * time.Second)
				} else {
					time.Sleep(5 * time.Second)
				}
				i++
			}
		}
		// If email is not provided, then prompt for it
		if email == "" {
			fmt.Print("Email: ")
			fmt.Scanln(&email)
		}
		// Let's check if the email is a valid email address
		var emailRegex = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
		if len(email) < 3 && len(email) > 254 {
			log.Fatalf("error: invalid email address: %s", email)
		}
		if !emailRegex.MatchString(email) {
			log.Fatalf("error: invalid email address: %s", email)
		}

		// If password is not provided, then prompt for it.
		if password == "" {
			fmt.Print("Password: ")
			bytesPassword, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				fmt.Printf("Error getting password from prompt: %s \n", err)
				os.Exit(1)
			}
			password = string(bytesPassword)
			fmt.Print("\n")
		}
		requireMFA, err2 := http.Login(email, password)
		if err2 != nil {
			log.Fatalf("error: %v", err2)
		}

		if requireMFA {
			if mfaCode == "" {
				fmt.Print("MFA Code: ")
				fmt.Scanln(&mfaCode)
				if len(mfaCode) < 6 && len(mfaCode) > 8 {
					log.Fatalf("error: mfa code: %s", mfaCode)
				}
			}

			err = http.MFAChallenge(strings.TrimSpace(mfaCode))
			mfaCode = ""
			if err != nil {
				log.Fatalf("error: %v", err)
			}
		}

		fmt.Println("Login successful")
	},
}

func init() {
	loginCmd.Flags().StringVarP(&email, "email", "e", "", "Email address")
	loginCmd.Flags().StringVarP(&password, "password", "p", "", "Password")
	loginCmd.Flags().StringVarP(&sso, "sso", "s", "sso", "SSO login")
	loginCmd.Flags().StringVarP(&mfaCode, "mfa", "m", "", "MFA  Code")
	// add hidden flag to disable browser opening
	loginCmd.Flags().BoolVar(&disableBrowser, "disable-browser", false, "Disable browser opening")
	// now make the disableBrowser flag hidden
	loginCmd.Flags().MarkHidden("disable-browser")

	rootCmd.AddCommand(loginCmd)
}
