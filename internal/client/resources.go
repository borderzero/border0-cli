package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/enum"
	osutil "github.com/borderzero/border0-cli/internal/util"
	"github.com/cenkalti/backoff/v4"
	"github.com/fatih/color"
	jwt "github.com/golang-jwt/jwt"
	"github.com/skratchdot/open-golang/open"
	"github.com/spf13/cobra"
)

var (
	// ErrResourceNotFound is returned when a fetched resource is not found
	ErrResourceNotFound = errors.New("resource not found")
)

// Login performs an OAuth2.0 client device authorization flow against the API
func Login(org string) (token string, claims jwt.MapClaims, err error) {
	if org == "" {
		err = errors.New("empty org not allowed")
		return
	}

	bodyBytes, err := json.Marshal(struct {
		Organization string `json:"organization"`
		DeviceOS     string `json:"device_os"`
	}{
		Organization: org,
		DeviceOS:     runtime.GOOS,
	})
	if err != nil {
		err = errors.New("unable to encode JSON request")
		return
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/client/device_authorizations", api.APIURL()), bytes.NewBuffer(bodyBytes))
	if err != nil {
		err = errors.New("unable to build new http request object")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		err = fmt.Errorf("failed to request client device authorization: %s", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("client device authorization request returned non-200 status: %d", resp.StatusCode)
		return
	}

	bodyByt, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("client device authorization response body could not be read: %s", err)
		return
	}

	var deviceAuthorizationReponse *struct {
		Code string `json:"code"`

		AuthorizationEndpoint               string `json:"authorization_endpoint"`
		AuthorizationEndpointCodeQueryParam string `json:"authorization_endpoint_code_query_param"`

		TokenEndpoint                string `json:"token_endpoint"`
		TokenEndpointCodeHeader      string `json:"token_endpoint_code_header"`
		TokenEndpointPollInterval    uint64 `json:"token_endpoint_poll_interval"`
		TokenEndpointPollMaxAttempts uint64 `json:"token_endpoint_poll_max_attempts"`
	}
	if err = json.Unmarshal(bodyByt, &deviceAuthorizationReponse); err != nil {
		err = fmt.Errorf("client device authorization response body not the correct shape: %s", err)
		return
	}

	url := fmt.Sprintf(
		"%s%s?%s=%s",
		api.APIURL(),
		deviceAuthorizationReponse.AuthorizationEndpoint,
		deviceAuthorizationReponse.AuthorizationEndpointCodeQueryParam,
		deviceAuthorizationReponse.Code)
	fmt.Printf("Please navigate to the URL below in order to complete the login process:\n%s\n", url)

	// Try opening the system's browser automatically. The error is ignored because the desired behavior of the
	// handler is the same regardless of whether opening the browser fails or succeeds -- we still print the URL.
	// This is desirable because in the event opening the browser succeeds, the customer may still accidentally
	// close the new tab / browser session, or may want to authenticate in a different browser / session. In the
	// event that opening the browser fails, the customer may still complete authenticating by navigating to the
	// URL in a different device.

	// check if we're on DARWIN and if we're running as sudo, if so, make sure we open the browser as the user
	// this prevents folsk from not having access to credentials , sessions, etc
	sudoUsername := os.Getenv("SUDO_USER")
	sudoAttempt := false
	if runtime.GOOS == "darwin" && sudoUsername != "" {
		err = exec.Command("sudo", "-u", sudoUsername, "open", url).Run()
		if err == nil {
			// This means, it's successull. So set sudo attemtp to True.
			sudoAttempt = true
		}
	}
	if !sudoAttempt {
		_ = open.Run(url)
	}

	token, err = pollForToken(
		org,
		deviceAuthorizationReponse.Code,
		deviceAuthorizationReponse.TokenEndpoint,
		deviceAuthorizationReponse.TokenEndpointCodeHeader,
		deviceAuthorizationReponse.TokenEndpointPollInterval,
		deviceAuthorizationReponse.TokenEndpointPollMaxAttempts)
	if err != nil {
		err = fmt.Errorf("polling for authorized token failed: %s", err)
		return
	}

	parsedJWT, err := jwt.Parse(token, nil)
	if parsedJWT == nil {
		err = fmt.Errorf("couldn't parse token: %w", err)
		return
	}

	claims = parsedJWT.Claims.(jwt.MapClaims)
	if _, ok := claims["user_email"]; !ok {
		err = errors.New("can't find claim for user_email")
		return
	}

	if err = saveToken(token); err != nil {
		err = fmt.Errorf("failed to save token: %s", err)
		return
	}

	return token, claims, nil
}

func pollForToken(
	org string,
	code string,
	tokenEndpoint string,
	tokenEndpointCodeHeader string,
	pollIntervalSeconds uint64,
	pollIntervalMaxAttempts uint64,
) (string, error) {
	var token string

	errUnauthorizedForOrg := fmt.Errorf("authenticated user is not authorized for organization \"%s\"", org)
	errUnexpectedStatus := errors.New("unexpected client device authorization status")
	errStillWaiting := errors.New("client device authorization flow not (yet) completed")

	retryFn := func() error {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s%s", api.APIURL(), tokenEndpoint), nil)
		if err != nil {
			return fmt.Errorf("failed to build new request: %s", err)
		}
		req.Header.Add(tokenEndpointCodeHeader, code)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed request for client device authorization code status: %s", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("non 200 back from api")
		}

		bodyByt, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("could not read response body for device auth: %s", err)
		}

		var clientDeviceAuthorizationResponse *struct {
			Status      string `json:"status,omitempty"`
			ClientToken string `json:"client_token,omitempty"`
		}
		if err := json.Unmarshal(bodyByt, &clientDeviceAuthorizationResponse); err != nil {
			return fmt.Errorf("could not decode response body onto client device authorization object: %s", err)
		}

		switch clientDeviceAuthorizationResponse.Status {
		case "authorized":
			token = clientDeviceAuthorizationResponse.ClientToken
			return nil
		case "not_authorized":
			return backoff.Permanent(errUnauthorizedForOrg)
		case "waiting":
			return errStillWaiting
		default:
			return errUnexpectedStatus
		}
	}

	err := backoff.Retry(retryFn,
		backoff.WithMaxRetries(
			backoff.NewConstantBackOff(time.Duration(pollIntervalSeconds)*time.Second),
			pollIntervalMaxAttempts,
		),
	)
	if err != nil {
		if errors.Is(err, errUnauthorizedForOrg) {
			fmt.Printf("Error: %s\n", err)
			os.Exit(1)
		}
		if errors.Is(err, errUnexpectedStatus) {
			fmt.Println("Error: An unknown error occured!")
			os.Exit(1)
		}
		if errors.Is(err, errStillWaiting) {
			fmt.Printf("Error: Device authorization flow timed out after %d seconds\n", pollIntervalSeconds*pollIntervalMaxAttempts)
			os.Exit(1)
		}

		// unhandled error cases are returned and eventually logged
		return "", err
	}

	return token, nil
}

func saveToken(token string) error {

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("couldn't get currently logged in operating system user: %w", err)
	}
	homedir, err := osutil.GetUserHomeDir()
	if err != nil {
		return fmt.Errorf("couldn't get user home directory: %w", err)
	}

	// check if this is being run as sudo, if so, use the sudo user's home dir
	sudoMode := false
	username := os.Getenv("SUDO_USER")
	if username != "" {
		sudoMode = true
		//create a new user struct
		currentUser, err = user.Lookup(username)
		if err != nil {
			return fmt.Errorf("couldn't get user details: %w", err)
		}
	}

	// Write to client token file
	tokenFile := ClientTokenFile(homedir)

	// create dir if not exists
	configPath := filepath.Dir(tokenFile)
	if _, err = os.Stat(configPath); os.IsNotExist(err) {
		if err = os.Mkdir(configPath, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s : %w", configPath, err)
		}
	}

	f, err := os.Create(tokenFile)
	if err != nil {
		return fmt.Errorf("couldn't write token: %w", err)
	}
	defer f.Close()
	if err = os.Chmod(tokenFile, 0600); err != nil {
		return fmt.Errorf("couldn't change permission for token file: %w", err)
	}

	// Make sure to change the owner of the token file to the user who ran the command
	if sudoMode {
		uid, err := strconv.Atoi(currentUser.Uid)
		if err != nil {
			return fmt.Errorf("couldn't convert UID to integer: %w", err)
		}

		gid, err := strconv.Atoi(currentUser.Gid)
		if err != nil {
			return fmt.Errorf("couldn't convert GID to integer: %w", err)
		}
		if err = os.Chown(tokenFile, uid, gid); err != nil {
			return fmt.Errorf("couldn't change owner for token file: %w", err)
		}
	}

	if _, err = f.WriteString(fmt.Sprintf("%s\n", token)); err != nil {
		return fmt.Errorf("couldn't write token to file: %w", err)
	}

	return nil
}

func IsExistingClientTokenValid(homeDir string) (valid bool, token, email string, err error) {
	if homeDir == "" {
		homeDir, err = osutil.GetUserHomeDir()
	}
	if err != nil {
		return
	}

	token, err = GetClientToken(homeDir)
	if err != nil {
		err = fmt.Errorf("couldn't get client token: %w", err)
		return
	}
	email, _, err = ValidateClientToken(token)
	return (err == nil), token, email, err
}

func GetClientToken(homeDir string) (string, error) {
	if os.Getenv("BORDER0_CLIENT_TOKEN") != "" {
		return os.Getenv("BORDER0_CLIENT_TOKEN"), nil
	}

	tokenFile := ClientTokenFile(homeDir)
	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		return "", fmt.Errorf("please login first (no token found in " + tokenFile + ")")
	}
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", err
	}

	tokenString := strings.TrimRight(string(content), "\n")
	return tokenString, nil
}

func ClientTokenFile(homedir string) string {
	tokenfile := ""
	if runtime.GOOS == "windows" {
		tokenfile = fmt.Sprintf("%s/.border0/client_token", os.Getenv("APPDATA"))
	} else {
		tokenfile = fmt.Sprintf("%s/.border0/client_token", homedir)
	}

	return tokenfile
}

func ValidateClientToken(token string) (email string, claims jwt.MapClaims, err error) {
	parsedJWT, err := jwt.Parse(token, nil)
	if parsedJWT == nil {
		err = fmt.Errorf("couldn't parse token: %w", err)
		return
	}

	claims = parsedJWT.Claims.(jwt.MapClaims)
	if _, ok := claims["user_email"]; ok {
		email = claims["user_email"].(string)
	} else {
		err = fmt.Errorf("can't find claim for user_email")
		return
	}

	now := time.Now().Unix()
	if !claims.VerifyExpiresAt(now, false) {
		exp := claims["exp"].(float64)
		delta := time.Unix(now, 0).Sub(time.Unix(int64(exp), 0))
		err = fmt.Errorf("token expired: token for %s expired %v ago", email, delta)
		return
	}
	return email, claims, nil
}

func FetchResources(token string, filteredTypes ...string) (resources models.ClientResources, err error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/client/resources", api.APIURL()), nil)
	req.Header.Add("x-access-token", token)
	client := http.Client{
		Timeout: 15 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		err = fmt.Errorf("couldn't request dnsrecords: %w", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		err = errors.New("no valid token, please login")
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to get DNS records.. HTTP code not 200 but %d", resp.StatusCode)
		return
	}

	if err = json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		err = fmt.Errorf("couldn't parse dnsrecords response: %w", err)
		return
	}
	if len(filteredTypes) > 0 {
		allowedTypes := make(map[string]struct{})
		for _, typ := range filteredTypes {
			allowedTypes[strings.ToLower(typ)] = struct{}{}
		}
		tmp := resources.Resources[:0] // use the same block of memory to reduce allocation cost
		for _, res := range resources.Resources {
			if _, exists := allowedTypes[strings.ToLower(res.SocketType)]; exists {
				tmp = append(tmp, res)
			}
		}
		resources.Resources = tmp
	}

	return resources, nil
}

func FetchResource(token string, name string) (resource models.ClientResource, err error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/client/resource/%s", api.APIURL(), name), nil)
	req.Header.Add("x-access-token", token)
	client := http.Client{
		Timeout: 15 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		err = fmt.Errorf("couldn't request resource: %w", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		err = errors.New("no valid token, please login")
		return
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
		err = fmt.Errorf("%w: %s", ErrResourceNotFound, name)
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to get resource HTTP code not 200 but %d", resp.StatusCode)
		return
	}

	if err = json.NewDecoder(resp.Body).Decode(&resource); err != nil {
		err = fmt.Errorf("couldn't parse resource response: %w", err)
		return
	}

	return resource, nil
}

func ReadTokenOrAskToLogIn() (token string, err error) {
	var valid bool
	valid, token, _, err = IsExistingClientTokenValid("")
	if !valid {
		fmt.Println(err)
		fmt.Println()

		var orgID string
		if err = survey.AskOne(&survey.Input{
			Message: "let's try to log in again, what is your organization id/email:",
		}, &orgID, survey.WithValidator(survey.Required)); err != nil {
			err = fmt.Errorf("couldn't collect organization id/email from input: %w", err)
			return
		}

		token, _, err = Login(orgID)
		if err != nil {
			err = fmt.Errorf("failed logging into org %s: %w", orgID, err)
			return
		}
	}
	return token, nil
}

func AutocompleteHost(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var hosts []string

	valid, token, _, err := IsExistingClientTokenValid("")
	if !valid || err != nil {
		return hosts, cobra.ShellCompDirectiveNoFileComp
	}

	resources, err := FetchResources(token, cmd.Name())
	if err != nil {
		return hosts, cobra.ShellCompDirectiveNoFileComp
	}

	toCompleteSlice := strings.SplitN(toComplete, "@", 2)
	host := toCompleteSlice[len(toCompleteSlice)-1]

	for _, res := range resources.Resources {
		for _, domain := range res.Domains {
			if strings.HasPrefix(domain, host) {
				var user string

				if len(toCompleteSlice) == 2 {
					user = fmt.Sprintf("%s@", toCompleteSlice[0])
				}
				hosts = append(hosts, fmt.Sprintf("%s%s", user, domain))

			}
		}
	}

	return hosts, cobra.ShellCompDirectiveNoFileComp
}

func EnterDBName(inputDBName, suggestedDBname string) (enteredDBName string, err error) {
	enteredDBName = inputDBName
	if enteredDBName == "" {
		if err = survey.AskOne(&survey.Input{
			Message: "what is the name of the database schema:",
			Default: suggestedDBname,
		}, &enteredDBName); err != nil {
			err = fmt.Errorf("couldn't capture database input: %w", err)
			return
		}
	}

	return enteredDBName, nil
}

func PickHost(inputHost string, socketTypes ...string) (models.ClientResource, error) {
	if inputHost != "" {
		valid, _, _, err := IsExistingClientTokenValid("")
		if !valid || err != nil {
			return models.ClientResource{
				Domains: []string{inputHost},
			}, nil
		}
	}

	token, err := ReadTokenOrAskToLogIn()
	if err != nil {
		return models.ClientResource{}, err
	}
	resources, err := FetchResources(token, socketTypes...)
	if err != nil {
		return models.ClientResource{}, fmt.Errorf("failed fetching client resources: %w", err)
	}

	if inputHost != "" {
		for _, res := range resources.Resources {
			if res.HasDomain(inputHost) {
				return res, nil
			}
		}
	}

	blue := color.New(color.FgBlue)
	answers := make(map[string]models.ClientResource)

	var hosts []string
	for _, res := range resources.Resources {
		hostToShow := res.DomainsToString() + " " + blue.Sprintf("[%s]", strings.Split(res.Description, ";")[0])
		answers[hostToShow] = res
		hosts = append(hosts, hostToShow)
	}

	if len(hosts) < 1 {
		return models.ClientResource{}, fmt.Errorf("No hosts available to connect to\n")
	}

	var picked string
	if err = survey.AskOne(&survey.Select{
		Message: "choose a host:",
		Options: hosts,
	}, &picked); err != nil {
		return models.ClientResource{}, fmt.Errorf("couldn't capture host input: %w", err)
	}
	return answers[picked], nil
}

func PickResourceTypes(inputFilter string) (pickedTypes []string, err error) {
	if inputFilter == "prompt" {
		allTypes := []string{enum.HTTPSocket, enum.TLSSocket, enum.SSHSocket, enum.DatabaseSocket}
		if err = survey.AskOne(&survey.MultiSelect{
			Message: "what types of resources would you like to see:",
			Options: allTypes,
			Default: allTypes,
		}, &pickedTypes); err != nil {
			err = fmt.Errorf("unable to capture input: %w", err)
			return
		}
	} else {
		pickedTypes = strings.Split(inputFilter, ",")
	}
	if len(pickedTypes) == 0 {
		err = errors.New("no resource types selected")
		return
	}
	for _, typ := range pickedTypes {
		if typ == enum.HTTPSocket {
			pickedTypes = append(pickedTypes, enum.HTTPSSocket)
		}
	}
	return pickedTypes, nil
}
