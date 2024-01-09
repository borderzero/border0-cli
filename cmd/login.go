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
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"os"

	"github.com/borderzero/border0-cli/internal"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/cenkalti/backoff/v4"
	jwt "github.com/golang-jwt/jwt"
	"github.com/mdp/qrterminal"
	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func pollForToken(sessionToken string) {
	exponentialBackoff := backoff.NewExponentialBackOff()
	exponentialBackoff.InitialInterval = 1 * time.Second
	exponentialBackoff.MaxInterval = 5 * time.Second
	exponentialBackoff.Multiplier = 1.3
	exponentialBackoff.MaxElapsedTime = 3 * time.Minute

	var token string

	retryFn := func() error {
		tk, err := http.GetDeviceAuthorization(sessionToken)
		if err != nil {
			return err
		}
		token = tk.Token
		return err
	}

	err := backoff.Retry(retryFn, exponentialBackoff)
	if err != nil {
		if errors.Is(err, http.ErrUnauthorized) {
			log.Fatalf("We couldn't log you in, your session is expired or you are not authorized to perform this action: %v", err)
		}
		log.Fatalf("We couldn't log you in, make sure that you are properly logged in using the link above: %v", err)
	}

	fmt.Println("Login successful")
	if err := http.SaveTokenInDisk(token); err != nil {
		log.Fatalf("failed to save token: %s", err)
	}
}

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
		if latest_version != internal.Version {
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

			if qr {
				fmt.Printf("Please scan the following QR code to complete the login process from a mobile device:\n%s\n", url)
				qrterminal.GenerateWithConfig(
					url,
					qrterminal.Config{
						Level:     qrterminal.L,
						Writer:    os.Stdout,
						QuietZone: 1,
						BlackChar: qrterminal.BLACK,
						WhiteChar: qrterminal.WHITE,
					},
				)
			} else {
				fmt.Printf("Please navigate to the URL below in order to complete the login process:\n%s\n", url)

				// Try opening the system's browser automatically. The error is ignored because the desired behavior of the
				// handler is the same regardless of whether opening the browser fails or succeeds -- we still print the URL.
				// This is desirable because in the event opening the browser succeeds, the customer may still accidentally
				// close the new tab / browser session, or may want to authenticate in a different browser / session. In the
				// event that opening the browser fails, the customer may still complete authenticating by navigating to the
				// URL in a different device.

				/// check if the disableBrowser flag is set
				if !disableBrowser {

					// check if we're on DARWIN and if we're running as sudo, if so, make sure we open the browser as the user
					// this prevents folsk from not having access to credentials , sessions, etc
					sudoUsername := os.Getenv("SUDO_USER")
					sudoAttempt := false
					if runtime.GOOS == "darwin" && sudoUsername != "" {
						err = exec.Command("sudo", "-u", sudoUsername, "open", url).Run()
						if err == nil {
							// If for some reason this failed, we'll try again to old way
							sudoAttempt = true
						}
					}
					if !sudoAttempt {
						_ = open.Run(url)
					}
				}
			}

			pollForToken(sessionToken)
			return
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
	loginCmd.Flags().BoolVar(&qr, "qr", false, "Print a QR code for authenticating with a mobile device")
	// now make the disableBrowser and qr flags hidden
	loginCmd.Flags().MarkHidden("disable-browser")
	loginCmd.Flags().MarkHidden("qr")
	loginCmd.Flags().MarkHidden("email")
	loginCmd.Flags().MarkHidden("password")

	rootCmd.AddCommand(loginCmd)
}
