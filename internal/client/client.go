package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/client/password"
	"github.com/borderzero/border0-cli/internal/util"
	jwt "github.com/golang-jwt/jwt"
	"github.com/moby/term"
	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"nhooyr.io/websocket"
)

var (
	ErrHandshakeFailed = errors.New("failed to authenticate against connector")
)

const (
	successURL            = "https://www.border0.com/logged-in"
	failURL               = "https://www.border0.com/fail-message"
	wsProxyToOriginHeader = "https://client.border0.com"
)

func CheckIfTokenIsExpired(rawToken string) bool {
	tempJWT, _ := jwt.Parse(rawToken, nil)

	if tempJWT != nil {
		claims := tempJWT.Claims.(jwt.MapClaims)
		exp := int64(claims["exp"].(float64))
		if exp-10 > time.Now().Unix() {
			return false
		}
	}
	return true
}

func MTLSLogin(logger *zap.Logger, hostname string) (string, jwt.MapClaims, error) {
	if hostname == "" {
		return "", nil, errors.New("empty hostname not allowed")
	}

	tokenFile := MTLSTokenFile()
	var token string

	if os.Getenv("BORDER0_CLIENT_TOKEN") != "" {
		token = os.Getenv("BORDER0_CLIENT_TOKEN")
		if CheckIfTokenIsExpired(token) {
			// verify if our os.variable supplied token is not expired, error out if it is
			return "", nil, errors.New("token from environment variable BORDER0_CLIENT_TOKEN is expired")
		}
	} else {
		if _, err := os.Stat(tokenFile); err == nil {
			content, _ := os.ReadFile(tokenFile)
			tokenString := strings.TrimRight(string(content), "\n")

			if CheckIfTokenIsExpired(tokenString) {
				// token is expired, we rely on refresh logic
				token = ""
			} else {
				// assing the token variable from token file
				token = tokenString
			}
		}
	}

	_, err := FetchResource(token, hostname)
	if err != nil {
		if errors.Is(err, ErrResourceNotFound) {
			return "", nil, err
		}
		token = ""
	}

	if token == "" {
		listener, err := net.Listen("tcp", "localhost:")
		if err != nil {
			return "", nil, fmt.Errorf("unable to start local http listener: %w", err)
		}

		localPort := listener.Addr().(*net.TCPAddr).Port
		url := fmt.Sprintf("%s/mtls-ca/socket/%s/auth?port=%d", api.APIURL(), hostname, localPort)
		token = Launch(logger, url, listener)

		// create dir if not exists
		configPath := filepath.Dir(tokenFile)
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if err := os.Mkdir(configPath, 0700); err != nil {
				return "", nil, fmt.Errorf("failed to create directory %s : %w", configPath, err)
			}
		}

		f, err := os.Create(tokenFile)
		if err != nil {
			return "", nil, fmt.Errorf("failed to create token: %w", err)
		}
		if err = os.Chmod(tokenFile, 0600); err != nil {
			return "", nil, fmt.Errorf("failed to write token: %w", err)
		}
		defer f.Close()
		if _, err = f.WriteString(fmt.Sprintf("%s\n", token)); err != nil {
			return "", nil, fmt.Errorf("failed to write token: %w", err)
		}
	}

	parsedJWT, err := jwt.Parse(token, nil)
	if parsedJWT == nil {
		return "", nil, fmt.Errorf("couldn't parse token: %w", err)
	}

	claims := parsedJWT.Claims.(jwt.MapClaims)
	if _, ok := claims["user_email"]; !ok {
		return "", nil, errors.New("can't find claim for user_email")
	}

	if _, ok := claims["org_id"]; !ok {
		return "", nil, errors.New("can't find claim for org_id")
	}

	if token == "" {
		return "", nil, errors.New("login failed")
	}

	return token, claims, nil
}

func ReadOrgCert(orgID string) (cert *x509.Certificate, key *rsa.PrivateKey, caCert *x509.Certificate, crtPath string, keyPath, caPath string, err error) {
	home, err := util.GetUserHomeDir()
	if err != nil {
		err = fmt.Errorf("error: failed to get homedir : %w", err)
		return
	}

	caPath = filepath.Join(home, ".border0", orgID+"-ca.crt")
	if _, err = os.Stat(caPath); os.IsNotExist(err) {
		err = fmt.Errorf("error: ca certificate file %s not found", caPath)
		return
	}

	crtPath = filepath.Join(home, ".border0", orgID+".crt")
	if _, err = os.Stat(crtPath); os.IsNotExist(err) {
		err = fmt.Errorf("error: certificate file %s not found", crtPath)
		return
	}

	keyPath = filepath.Join(home, ".border0", orgID+".key")
	if _, err = os.Stat(crtPath); os.IsNotExist(err) {
		err = fmt.Errorf("error: key file %s not found", keyPath)
		return
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		err = fmt.Errorf("error: failed to read key file : %w", err)
		return
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		err = fmt.Errorf("error: failed to decode certificate file : %w", err)
		return
	}

	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		err = fmt.Errorf("error: failed to parse key file : %w", err)
		return
	}

	key, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		err = fmt.Errorf("error: failed to parse key file")
		return
	}

	certPEM, err := os.ReadFile(crtPath)
	if err != nil {
		err = fmt.Errorf("error: failed to read certificate file : %w", err)
		return
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		err = fmt.Errorf("error: failed to decode certificate file : %w", err)
		return
	}

	cert, err = x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		err = fmt.Errorf("error: failed to parse certificate file : %w", err)
		return
	}

	caCertPEM, err := os.ReadFile(caPath)
	if err != nil {
		err = fmt.Errorf("error: failed to read ca certificate file : %w", err)
		return
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		err = fmt.Errorf("error: failed to decode ca certificate file : %w", err)
		return
	}

	caCert, err = x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		err = fmt.Errorf("error: failed to parse ca certificate file : %w", err)
		return
	}

	return
}

func WriteCertToFile(cert *CertificateResponse, socketDNS string) (crtPath, keyPath, caPath string, err error) {
	home, err := util.GetUserHomeDir()
	if err != nil {
		err = fmt.Errorf("error: failed to get homedir : %w", err)
		return
	}

	// create dir if not exists
	dotDir := filepath.Join(home, ".border0")
	if _, err = os.Stat(dotDir); os.IsNotExist(err) {
		if err = os.Mkdir(dotDir, 0700); err != nil {
			err = fmt.Errorf("error: failed to create directory %s : %w", dotDir, err)
			return
		}
	}

	caPath = filepath.Join(dotDir, socketDNS+"-ca.crt")
	crtPath = filepath.Join(dotDir, socketDNS+".crt")
	keyPath = filepath.Join(dotDir, socketDNS+".key")

	if err = os.WriteFile(keyPath, []byte(cert.PrivateKey), 0600); err != nil {
		err = fmt.Errorf("error: failed to write key file : %w", err)
		return
	}

	if err = os.WriteFile(crtPath, []byte(cert.Certificate), 0600); err != nil {
		err = fmt.Errorf("error: failed to write certificate file : %w", err)
		return
	}

	if err = os.WriteFile(caPath, []byte(cert.CaCertificate), 0600); err != nil {
		err = fmt.Errorf("error: failed to write ca certificate file : %w", err)
		return
	}

	return crtPath, keyPath, caPath, nil
}

func GetSocketPort(name string, token string) (socketPort int, err error) {
	resource, err := FetchResource(token, name)

	if err != nil {
		return socketPort, err
	}

	return resource.SocketPorts[0], nil
}

func OrgIDFromToken() (orgID string) {
	tokenfile := MTLSTokenFile()
	if _, err := os.Stat(tokenfile); os.IsNotExist(err) {
		return
	} else {
		content, _ := os.ReadFile(tokenfile)
		if err == nil {
			tokenString := strings.TrimRight(string(content), "\n")
			jwtToken, _ := jwt.Parse(tokenString, nil)
			if jwtToken != nil {
				claims := jwtToken.Claims.(jwt.MapClaims)

				if _, ok := claims["org_id"]; ok {
					orgID = claims["org_id"].(string)
				}
			}
		}
	}

	return
}

func IsClientCertValid() (crtPath, keyPath string, valid bool) {
	orgID := OrgIDFromToken()

	if orgID == "" {
		return
	}

	cert, _, _, crtPath, keyPath, _, err := ReadOrgCert(orgID)
	if err != nil {
		return
	}

	if time.Now().Before(cert.NotAfter) && time.Now().After(cert.NotBefore) {
		valid = true
	}

	return
}

func FetchCertAndReturnPaths(logger *zap.Logger, hostname string) (crtPath, keyPath, caPath string, err error) {
	token, claims, err := MTLSLogin(logger, hostname)
	if err != nil {
		return
	}

	userEmail := fmt.Sprint(claims["user_email"])
	orgID := fmt.Sprint(claims["org_id"])

	cert := GetCert(token, userEmail)
	crtPath, keyPath, caPath, err = WriteCertToFile(cert, orgID)
	if err != nil {
		return
	}

	return
}

type ResourceInfo struct {
	Certficate                     *x509.Certificate
	PrivateKey                     *rsa.PrivateKey
	CaCertificate                  *x509.Certificate
	CertificatePath                string
	PrivateKeyPath                 string
	CaCertificatePath              string
	Port                           int
	ConnectorAuthenticationEnabled bool
	EndToEndEncryptionEnabled      bool
}

func (info *ResourceInfo) SetupTLSCertificate() tls.Certificate {
	return tls.Certificate{
		Certificate: [][]byte{info.Certficate.Raw},
		PrivateKey:  info.PrivateKey,
	}
}

func GetResourceInfo(logger *zap.Logger, hostname string) (info ResourceInfo, err error) {
	var claims jwt.MapClaims

	token, claims, err := MTLSLogin(logger, hostname)
	if err != nil {
		return
	}

	var ok bool
	if info.CertificatePath, info.PrivateKeyPath, ok = IsClientCertValid(); !ok {
		info.CertificatePath, info.PrivateKeyPath, info.CaCertificatePath, err = FetchCertAndReturnPaths(logger, hostname)
		if err != nil {
			return
		}
	}

	resource, err := FetchResource(token, hostname)
	if err != nil {
		return
	}

	info.Port = resource.SocketPorts[0]
	info.ConnectorAuthenticationEnabled = resource.ConnectorAuthenticationEnabled
	info.EndToEndEncryptionEnabled = resource.EndToEndEncryptionEnabled

	info.Certficate, info.PrivateKey, info.CaCertificate, _, _, _, err = ReadOrgCert(claims["org_id"].(string))
	if err != nil {
		return
	}

	return
}

func MTLSTokenFile() string {
	home, err := util.GetUserHomeDir()
	if err != nil {
		log.Fatalln("Unable to determine the user's home directory", err)
	}
	if runtime.GOOS == "windows" {
		home = os.Getenv("APPDATA")
	}
	return filepath.Join(home, ".border0/client_token")
}

func Launch(logger *zap.Logger, url string, listener net.Listener) string {
	c := make(chan string)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		url := r.URL
		q := url.Query()

		w.Header().Set("Content-Type", "text/html")

		if q.Get("token") != "" {
			w.Header().Set("Location", successURL)
			w.WriteHeader(302)
			c <- q.Get("token")
		} else {
			if q.Get("error") == "org_not_found" {
				w.Header().Set("Location", failURL)

			} else {
				w.Header().Set("Location", failURL)
			}
			w.WriteHeader(302)
			c <- ""
		}
	})

	srv := &http.Server{
		Handler: mux,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer srv.Shutdown(ctx)

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error: unable to start login process - %s", err)
		}
	}()

	var token string
	if openBrowser(url) {
		select {
		case token = <-c:
			if token == "" {
				log.Fatalln("Error: login failed")
			}
			return token
		case <-time.After(60 * time.Second):
			log.Fatalln("timeout during login")
		}
	}
	if token == "" {
		log.Fatalln("Error: login failed")
	}
	return token
}

func openBrowser(url string) bool {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"cmd", "/c", "start"}
	default:
		args = []string{"xdg-open"}
	}

	cmd := exec.Command(args[0], append(args[1:], url)...)
	return cmd.Start() == nil
}

type CertificateSigningRequest struct {
	Csr string `json:"csr"`
}

type CertificateResponse struct {
	PrivateKey    string `json:"client_private_key,omitempty"`
	Certificate   string `json:"client_certificate,omitempty"`
	CaCertificate string `json:"ca_certificate,omitempty"`
}

func GetCert(token string, email string) *CertificateResponse {
	// generate key
	keyBytes, _ := rsa.GenerateKey(rand.Reader, 2048)

	// generate csr
	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: email,
		},
		EmailAddresses: []string{email},
	}

	csrBytes, _ := x509.CreateCertificateRequest(rand.Reader, &template, keyBytes)
	csrPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	privateKeyBytes, _ := x509.MarshalPKCS8PrivateKey(keyBytes)
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privateKeyBytes})

	// sign cert request
	jv, _ := json.Marshal(CertificateSigningRequest{Csr: string(csrPem)})
	body := bytes.NewBuffer(jv)
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/organizations/csr", api.APIURL()), body)
	req.Header.Add("x-access-token", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error in request: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		log.Fatalln("Error: No valid token, Please login")
	}

	if resp.StatusCode != 200 {
		log.Fatalln("Error: Failed to get cert")
	}

	cert := &CertificateResponse{}
	err = json.NewDecoder(resp.Body).Decode(cert)
	if err != nil {
		log.Fatalln("Error: Failed to decode certificate")
	}

	cert.PrivateKey = string(privateKey)

	return cert
}

type SSHSignRequest struct {
	SSHPublicKey string `json:"ssh_public_key"`
}

type SSHSignResponse struct {
	SSHCertSigned string `json:"signed_ssh_cert"`
}

func validSshCert(certFile string, keyFile string) *SSHSignResponse {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return nil
	}

	sshCert, err := os.ReadFile(certFile)
	if err != nil {
		return nil
	}

	if _, err = os.Stat(keyFile); os.IsNotExist(err) {
		return nil
	}

	sshKeyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil
	}

	sshKey, err := ssh.ParsePrivateKey(sshKeyData)
	if err != nil {
		return nil
	}

	pubcert, _, _, _, err := ssh.ParseAuthorizedKey(sshCert)
	if err != nil {
		return nil
	}

	cert, ok := pubcert.(*ssh.Certificate)
	if !ok {
		return nil
	}

	_, err = ssh.NewCertSigner(cert, sshKey)
	if err != nil {
		return nil
	}

	if time.Now().Unix() > int64(cert.ValidAfter) && time.Now().Unix() < int64(cert.ValidBefore) {
		return &SSHSignResponse{SSHCertSigned: string(sshCert)}
	}

	return nil
}

func GenSSHKey(token, orgID, hostname string) (*SSHSignResponse, error) {
	_, err := FetchResource(token, hostname)
	if err != nil {
		return nil, fmt.Errorf("invalid resource: %s", err)
	}
	home, err := util.GetUserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to write ssh key: %v", err)
	}

	// check existing key is still valid
	sshCertPath := filepath.Join(home, ".ssh", fmt.Sprintf("%s-cert.pub", orgID))
	sshKeyPath := filepath.Join(home, ".ssh", orgID)
	sshCert := validSshCert(sshCertPath, sshKeyPath)

	if sshCert != nil {
		return sshCert, nil
	}

	if _, err := os.Stat(filepath.Join(home, ".ssh")); os.IsNotExist(err) {
		err := os.Mkdir(filepath.Join(home, ".ssh"), 0700)
		if err != nil {
			return nil, fmt.Errorf("failed to create ssh directory: %s", err)
		}
	}

	// create ssh key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh key: %v", err)
	}

	parsed, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh key: %v", err)
	}

	// write key
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: parsed})
	err = os.WriteFile(sshKeyPath, keyPem, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to write ssh key: %v", err)
	}

	// create public key
	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create public ssh key: %v", err)
	}
	data := ssh.MarshalAuthorizedKey(pub)

	//post signing request
	jv, _ := json.Marshal(SSHSignRequest{SSHPublicKey: strings.TrimRight(string(data), "\n")})
	body := bytes.NewBuffer(jv)
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/organizations/sign_ssh_key", api.APIURL()), body)
	req.Header.Add("x-access-token", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to sign key: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		log.Fatalln("error: no valid token, Please login")
	}

	if resp.StatusCode != 200 {
		log.Fatalln("error: failed to get cert")
	}

	cert := &SSHSignResponse{}
	err = json.NewDecoder(resp.Body).Decode(cert)
	if err != nil {
		log.Fatalln("error: failed to decode certificate")
	}

	err = os.WriteFile(sshCertPath, []byte(cert.SSHCertSigned), 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to write ssh key: %w", err)
	}

	return cert, nil
}

func ExecCommand(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func FindWindowsExecutable(parentDir, contains, suffix string) string {
	var (
		latestPath    string
		latestModTime time.Time
	)

	_ = filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check if path matches the pattern
		if strings.Contains(path, contains) && strings.HasSuffix(path, suffix) {
			// Found the DataGrip executable
			if info.ModTime().After(latestModTime) {
				latestPath = path
				latestModTime = info.ModTime()
			}
		}

		return nil
	})

	return latestPath
}

func CertToKeyStore(cert *x509.Certificate, key *rsa.PrivateKey) (ks keystore.KeyStore, pass []byte, err error) {
	// for more about keystore and jdbc to mysql connection with ssl, see:
	// https://dev.mysql.com/doc/connector-j/8.0/en/connector-j-reference-using-ssl.html
	ks = keystore.New()

	// privateKeyBlock, _ := pem.Decode([]byte(cert.PrivateKey))
	// if privateKeyBlock == nil {
	// 	err = errors.New("private key should have at least one pem block")
	// 	return
	// }
	// certificateBlock, _ := pem.Decode([]byte(cert.Certificate))
	// if certificateBlock == nil {
	// 	err = errors.New("certificate should have at least one pem block")
	// 	return
	// }

	keyData, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return
	}

	entry := keystore.PrivateKeyEntry{
		CreationTime: time.Now(),
		PrivateKey:   keyData,
		CertificateChain: []keystore.Certificate{
			{
				Type:    "X509",
				Content: cert.Raw,
			},
		},
	}

	pass = password.KeyStore()
	if err = ks.SetPrivateKeyEntry("border0", entry, pass); err != nil {
		err = fmt.Errorf("error setting encrypted private key to keystore: %w", err)
		return
	}

	return ks, pass, nil
}

func WriteKeyStore(ks keystore.KeyStore, filename string, password []byte) {
	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}()

	err = ks.Store(f, password)
	if err != nil {
		log.Fatal(err) // nolint: gocritic
	}
}

func Zeroing(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

func DownloadCertificateChain(hostname string) (certChainPath string, err error) {
	home, err := util.GetUserHomeDir()
	if err != nil {
		err = fmt.Errorf("failed to get home dir: %w", err)
		return
	}

	certChainPath = filepath.Join(home, ".border0", fmt.Sprintf("%s.chain.crt", hostname))

	// now let's download the self-signed root CA cert from letsencrypt.org
	httpClient := http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			r.URL.Opaque = r.URL.Path
			return nil
		},
		Timeout: 10 * time.Second,
	}
	resp, err := httpClient.Get("https://letsencrypt.org/certs/isrgrootx1.pem")
	if err != nil {
		err = fmt.Errorf("failed to download root CA cert: %w", err)
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("failed to download root CA cert: %w", err)
		return
	}

	if err = os.WriteFile(certChainPath, bodyBytes, 0600); err != nil {
		err = fmt.Errorf("failed to write root CA cert to the certificate file: %w", err)
		return
	}
	return
}

// TermSize gets the current window size and returns it in a window-change friendly format.
func TermSize(fd uintptr) []byte {
	size := make([]byte, 16)

	winsize, err := term.GetWinsize(fd)
	if err != nil {
		binary.BigEndian.PutUint32(size, uint32(80))
		binary.BigEndian.PutUint32(size[4:], uint32(24))
		return size
	}

	binary.BigEndian.PutUint32(size, uint32(winsize.Width))
	binary.BigEndian.PutUint32(size[4:], uint32(winsize.Height))

	return size
}

func OnInterruptDo(action func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		defer signal.Stop(sigChan)
		<-sigChan
		action()
		os.Exit(1)
	}()
}

func StartConnectorAuthListener(hostname string, port int, certificate tls.Certificate, caCertificate *x509.Certificate, localPort int, connectorAuthenticationEnabled bool, endToEndEncryptionEnabled bool, wsProxy string) (int, error) {
	systemCertPool, err := x509.SystemCertPool()
	if err != nil {
		return 0, fmt.Errorf("failed to load system cert pool: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      systemCertPool,
		ServerName:   hostname,
	}

	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", localPort))
	if err != nil {
		return 0, fmt.Errorf("unable to start local TLS listener, %s", err)
	}

	addr := fmt.Sprintf("%s:%d", hostname, port)

	go func() {
		defer l.Close()
		for {
			lcon, err := l.Accept()
			if err != nil {
				log.Fatalf("Listener: Accept Error: %s\n", err)
			}

			go func() {
				conn, err := Connect(addr, false, tlsConfig, certificate, caCertificate, connectorAuthenticationEnabled, endToEndEncryptionEnabled, wsProxy)
				if err != nil {
					log.Fatalf("failed to connect: %s\n", err)
				}

				handleConnection(lcon, conn)
			}()
		}
	}()

	return l.Addr().(*net.TCPAddr).Port, nil
}

func Connect(addr string, tlsNeeded bool, tlsConfig *tls.Config, certificate tls.Certificate, caCert *x509.Certificate, connectorAuthenticationEnabled bool, endToEndEncryptionEnabled bool, wsProxy string) (net.Conn, error) {
	var conn net.Conn
	if wsProxy != "" {
		wsConn, err := ConnectWSProxy(wsProxy, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to ws proxy: %w", err)
		}

		conn = wsConn
	} else {
		netConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return nil, err
		}
		conn = netConn
	}

	if tlsNeeded || connectorAuthenticationEnabled || endToEndEncryptionEnabled {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("failed to handshake with proxy: %w", err)
		}

		conn = tlsConn
	}

	return connectWithConn(conn, certificate, caCert, connectorAuthenticationEnabled, endToEndEncryptionEnabled)
}

func connectWithConn(conn net.Conn, certificate tls.Certificate, caCert *x509.Certificate, connectorAuthenticationEnabled bool, endToEndEncryptionEnabled bool) (net.Conn, error) {
	if !connectorAuthenticationEnabled && !endToEndEncryptionEnabled {
		return conn, nil
	}

	var tlsConfig *tls.Config
	if endToEndEncryptionEnabled {
		caCertPool := x509.NewCertPool()
		caCertPool.AddCert(caCert)

		tlsConfig = &tls.Config{
			Certificates:       []tls.Certificate{certificate},
			RootCAs:            caCertPool,
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				// Convert raw certs to x509.Certificate
				certs := make([]*x509.Certificate, len(rawCerts))
				for i, asn1Data := range rawCerts {
					cert, err := x509.ParseCertificate(asn1Data)
					if err != nil {
						return errors.New("failed to parse certificate from server: " + err.Error())
					}
					certs[i] = cert
				}

				// Use the root CA to verify the certificate chain
				opts := x509.VerifyOptions{
					Roots: caCertPool,
				}
				_, err := certs[0].Verify(opts)
				return err
			},
		}
	} else {
		tlsConfig = &tls.Config{
			Certificates:       []tls.Certificate{certificate},
			InsecureSkipVerify: true,
		}
	}

	connectorConn := tls.Client(conn, tlsConfig)
	if err := connectorConn.Handshake(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHandshakeFailed, err)
	}

	if endToEndEncryptionEnabled {
		return connectorConn, nil
	}

	_, err := conn.Write([]byte("BORDER0-CLIENT-CONNECTOR-AUTHENTICATED"))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write to proxy: %w", err)
	}

	return conn, nil
}

func handleConnection(src net.Conn, dst net.Conn) {
	defer src.Close()
	defer dst.Close()

	chDone := make(chan bool, 1)

	go func() {
		io.Copy(src, dst)
		chDone <- true
	}()

	go func() {
		io.Copy(dst, src)
		chDone <- true
	}()

	<-chDone
}

func ConnectWSProxy(proxyUrl string, addr string) (net.Conn, error) {
	hostname, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to split host and port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("failed to convert port to int: %w", err)
	}

	destination := struct {
		DNSName string `json:"dnsname"`
		Port    int    `json:"port"`
	}{
		DNSName: hostname,
		Port:    port,
	}

	destinationJson, err := json.Marshal(&destination)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal destination: %w", err)
	}

	parsedURL, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse wsproxy url: %w", err)
	}

	parsedURL.RawQuery = url.Values{
		"dst": []string{base64.StdEncoding.EncodeToString(destinationJson)},
	}.Encode()

	wsURL := parsedURL.String()

	httpHeader := http.Header{}
	httpHeader.Set("Origin", wsProxyToOriginHeader)

	ctx := context.TODO()
	wsConn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: httpHeader})
	if err != nil {
		return nil, fmt.Errorf("failed to perform WebSocket handshake on %s: %w", wsURL, err)
	}

	wsNetConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)

	return wsNetConn, nil
}
