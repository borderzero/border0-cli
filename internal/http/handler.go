package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	jwt "github.com/golang-jwt/jwt"
)

const (
	download_url = "https://download.border0.com"
)

var ErrUnauthorized = errors.New("unaouthorized")

type ErrorMessage struct {
	ErrorMessage string `json:"error_message,omitempty"`
}

type Client struct {
	token   string
	version string
}

func WebUrl() string {
	if os.Getenv("BORDER0_WEB_URL") != "" {
		return os.Getenv("BORDER0_WEB_URL")
	} else {
		return "https://portal.border0.com"
	}
}

func TokenFilePath() string {
	return tokenfile()
}

func tokenfile() string {
	tokenfile := ""
	if runtime.GOOS == "windows" {
		tokenfile = fmt.Sprintf("%s/.border0/token", os.Getenv("APPDATA"))
	} else {
		tokenfile = fmt.Sprintf("%s/.border0/token", os.Getenv("HOME"))
	}
	return tokenfile
}

func NewClient() (*Client, error) {
	token, err := GetToken()
	if err != nil {
		return nil, err
	}

	c := &Client{token: token}

	return c, nil
}

func NewClientWithAccessToken(token string) (*Client, error) {
	var accessToken string

	if token != "" {
		accessToken = token
	} else {
		token, err := GetToken()
		if err != nil {
			return nil, err
		}
		accessToken = token
	}

	c := &Client{token: accessToken}

	return c, nil
}

func (c *Client) WithVersion(version string) *Client {
	if version == "" {
		return c
	}
	c2 := new(Client)
	*c2 = *c
	c2.version = version
	return c2
}

func (c *Client) WithAccessToken(token string) *Client {
	if token == "" {
		return c
	}
	c2 := new(Client)
	*c2 = *c
	c2.token = token
	return c2
}

func (c *Client) Request(method string, url string, target interface{}, data interface{}) error {
	jv, _ := json.Marshal(data)
	body := bytes.NewBuffer(jv)

	req, err := http.NewRequest(method, fmt.Sprintf("%s/%s", api.APIURL(), url), body)
	if err != nil {
		return fmt.Errorf("failed to create new http request object: %s", err)
	}
	req.Header.Add("x-access-token", c.token)
	req.Header.Add("x-client-requested-with", "border0")
	if c.version != "" {
		req.Header.Add("x-client-version", c.version)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("no valid token, Please login")
	}

	if resp.StatusCode == http.StatusNotFound {
		return errors.New("not found")
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode > http.StatusNoContent {
		responseData, err := io.ReadAll(resp.Body)
		if err != nil {
			// return just status code if failed to read response body
			return fmt.Errorf("api returned a non 2xx status code (%d)", resp.StatusCode)
		}

		type baseError struct {
			ErrorMessage string `json:"error_message,omitempty"`
			StatusCode   int    `json:"status_code,omitempty"`
		}

		var errorResponse baseError
		if err = json.Unmarshal(responseData, &errorResponse); err != nil {
			// return status code and raw response (as string) if failed to decode to JSON object
			return fmt.Errorf("api returned a non 2xx status code (%d) with body: %s", resp.StatusCode, string(responseData))
		}

		// return status code and api error message if decoding to baseError struct succeeded
		return fmt.Errorf("api returned a non 2xx status code (%d) with error message: %s", resp.StatusCode, errorResponse.ErrorMessage)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if target != nil {
		err = json.NewDecoder(resp.Body).Decode(target)
		if err != nil {
			return errors.New("failed to decode data")
		}
	}

	return nil
}

func MFAChallenge(code string) error {
	c, err := NewClient()
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	form := models.MfaForm{Code: code}
	res := models.TokenForm{}

	err = c.Request(http.MethodPost, "users/mfa_challenge", &res, &form)
	if err != nil {
		return err
	}

	c.token = res.Token

	f, err := os.Create(tokenfile())
	if err != nil {
		return err
	}

	if err := os.Chmod(tokenfile(), 0600); err != nil {
		return err
	}

	defer f.Close()
	_, err2 := f.WriteString(fmt.Sprintf("%s\n", c.token))
	if err2 != nil {
		return err2
	}

	return nil
}

func CreateDeviceAuthorization() (string, error) {
	resp, err := http.Post(fmt.Sprintf("%s/device_authorizations", api.APIURL()), "application/json", nil)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		responseData, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unauthorized %v", string(responseData))
	}

	if resp.StatusCode != http.StatusOK {
		var errorMessage ErrorMessage
		json.NewDecoder(resp.Body).Decode(&errorMessage)

		return "", fmt.Errorf(errorMessage.ErrorMessage)
	}

	type sessionToken struct {
		Token string `json:"token,omitempty"`
	}

	var ssToken sessionToken
	json.NewDecoder(resp.Body).Decode(&ssToken)

	if ssToken.Token != "" {
		return ssToken.Token, nil
	}

	return "", errors.New("couldn't fetch the temporary token")
}

func Login(email, password string) (bool, error) {
	c := &Client{}
	form := models.LoginForm{Email: email, Password: password}
	buf, err := json.Marshal(form)
	if err != nil {
		return false, err
	}

	requestReader := bytes.NewReader(buf)

	resp, err := http.Post(fmt.Sprintf("%s/login", api.APIURL()), "application/json", requestReader)
	if err != nil {
		return false, err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return false, errors.New("Login failed")
	}

	if resp.StatusCode != http.StatusOK {
		return false, errors.New("failed to login")
	}

	res := models.TokenForm{}
	json.NewDecoder(resp.Body).Decode(&res)

	c.token = res.Token

	if err := SaveTokenInDisk(c.token); err != nil {
		return false, err
	}

	return res.MFA, nil
}

func SaveTokenInDisk(accessToken string) error {
	// create dir if not exists
	configPath := filepath.Dir(tokenfile())
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.Mkdir(configPath, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s : %s", configPath, err)
		}
	}

	f, err := os.Create(tokenfile())
	if err != nil {
		return err
	}

	if err := os.Chmod(tokenfile(), 0600); err != nil {
		return err
	}

	defer f.Close()
	_, err2 := f.WriteString(fmt.Sprintf("%s\n", accessToken))
	if err2 != nil {
		return err2
	}

	return nil
}

func Register(name, email, password string) error {
	form := models.RegisterForm{Name: name, Email: email, Password: password}
	buf, err := json.Marshal(form)
	if err != nil {
		return err
	}
	requestReader := bytes.NewReader(buf)
	resp, err := http.Post(fmt.Sprintf("%s/user", api.APIURL()), "application/json", requestReader)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		responseData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to register user %d\n%v", resp.StatusCode, string(responseData))
	}
	return nil
}

func GetLatestVersion() (string, error) {
	resp, err := http.Get(fmt.Sprintf("%s/latest_version.txt", download_url))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("version check failed. Failed to get latest version (%d)", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	bodyString := string(bodyBytes)
	version := strings.TrimSpace(string(bodyString))
	version = strings.TrimSuffix(version, "\n")
	return version, nil
}

func GetLatestBinary(osname string, osarch string) (string, []byte, error) {
	var bin_url string
	var checksum_url string
	switch osname {
	case "darwin":
		if osarch == "amd64" {
			bin_url = download_url + "/darwin_amd64/border0"
			checksum_url = download_url + "/darwin_amd64/sha256-checksum.txt"
		} else if osarch == "arm64" {
			bin_url = download_url + "/darwin_arm64/border0"
			checksum_url = download_url + "/darwin_arm64/sha256-checksum.txt"
		}
	case "linux":
		if osarch == "arm64" {
			bin_url = download_url + "/linux_arm64/border0"
			checksum_url = download_url + "/linux_arm64/sha256-checksum.txt"
		} else if osarch == "arm" {
			bin_url = download_url + "/linux_arm/border0"
			checksum_url = download_url + "/linux_arm/sha256-checksum.txt"
		} else {
			bin_url = download_url + "/linux_amd64/border0"
			checksum_url = download_url + "/linux_amd64/sha256-checksum.txt"
		}
	case "windows":
		bin_url = download_url + "/windows_amd64/border0.exe"
		checksum_url = download_url + "/windows_amd64/sha256-checksum.txt"
	default:
		return "", nil, fmt.Errorf("unknown OS: %s", osname)
	}

	// Download checksum
	resp, err := http.Get(checksum_url)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("failed to get latest checksum version (%d)", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}

	bodyString := string(bodyBytes)
	checksum := strings.TrimSpace(string(bodyString))
	checksum = strings.TrimSuffix(checksum, "\n")

	// Download binary
	resp, err = http.Get(bin_url)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("failed to get latest version (%d)", resp.StatusCode)
	}

	bodyBytes, err2 := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err2
	}
	return checksum, bodyBytes, nil
}

func GetToken() (string, error) {
	if os.Getenv("BORDER0_ADMIN_TOKEN") != "" {
		return os.Getenv("BORDER0_ADMIN_TOKEN"), nil
	}

	if _, err := os.Stat(tokenfile()); os.IsNotExist(err) {
		return "", errors.New("please login first (no token found)")
	}
	content, err := os.ReadFile(tokenfile())
	if err != nil {
		return "", err
	}

	tokenString := strings.TrimRight(string(content), "\n")
	return tokenString, nil
}

func GetTunnel(socketID string, tunnelID string) (*models.Tunnel, error) {
	tunnel := models.Tunnel{}
	token, err := GetToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf("%s/socket/%s/tunnel/%s", api.APIURL(), socketID, tunnelID),
		nil,
	)
	if err != nil {
		return nil, err
	}

	req.Header.Add("x-access-token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get tunnel (%d)", resp.StatusCode)
	}

	err = json.NewDecoder(resp.Body).Decode(&tunnel)
	if err != nil {
		return nil, errors.New("failed to decode tunnel response")
	}
	return &tunnel, nil
}

func GetDeviceAuthorization(sessionToken string) (*models.SessionTokenForm, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/device_authorizations", api.APIURL()), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create new http request object: %s", err)
	}
	req.Header.Add("x-access-token", sessionToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get device_authorization (%d)", resp.StatusCode)
	}

	var form models.SessionTokenForm
	err = json.NewDecoder(resp.Body).Decode(&form)
	if err != nil {
		return nil, errors.New("failed to decode device auth response")
	}
	return &form, nil
}

func GetUserID() (*string, *string, error) {
	tokenStr, err := GetToken()
	if err != nil {
		return nil, nil, err
	}

	token, err := jwt.Parse(tokenStr, nil)
	if token == nil {
		return nil, nil, err
	}

	claims, _ := token.Claims.(jwt.MapClaims)
	tokenUserId := fmt.Sprintf("%v", claims["user_id"])
	userID := strings.ReplaceAll(tokenUserId, "-", "")

	return &userID, &tokenUserId, nil
}

func GetUserIDFromAccessToken(accessToken string) (*string, *string, error) {
	var rawToken string
	if accessToken != "" {
		rawToken = accessToken
	} else {
		tokenStr, err := GetToken()
		if err != nil {
			return nil, nil, err
		}
		rawToken = tokenStr
	}

	token, err := jwt.Parse(rawToken, nil)
	if token == nil {
		return nil, nil, err
	}

	claims, _ := token.Claims.(jwt.MapClaims)
	tokenUserId := fmt.Sprintf("%v", claims["user_id"])
	userID := strings.ReplaceAll(tokenUserId, "-", "")

	return &userID, &tokenUserId, nil
}
