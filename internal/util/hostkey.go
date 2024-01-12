package util

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/user"

	"golang.org/x/crypto/ssh"
)

const (
	serviceConfigPath = "/etc/border0/"
	sshHostKeyFile    = "ssh_host_ecdsa_key"
)

// Hostkey returns the ssh signer for the connector host.
func Hostkey() (*ssh.Signer, error) {
	var errors []error

	if _, err := os.Stat(serviceConfigPath + sshHostKeyFile); err == nil {
		keyFilePath := serviceConfigPath + sshHostKeyFile
		signer, err := signer(keyFilePath)
		if err != nil {
			errors = append(errors, err)
		} else {
			return signer, nil
		}
	}

	u, err := user.Current()
	if err == nil {
		if _, err := os.Stat(u.HomeDir + "/.border0/" + sshHostKeyFile); err == nil {
			keyFilePath := u.HomeDir + "/.border0/" + sshHostKeyFile
			signer, err := signer(keyFilePath)
			if err != nil {
				errors = append(errors, err)
			} else {
				return signer, nil
			}
		}
	}

	// no existing host key found, generate a new one
	signer, err := generateHostkey()
	if err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return signer, fmt.Errorf("%s", errors)
	}

	return signer, nil
}

func generateHostkey() (*ssh.Signer, error) {
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate host key: %w", err)
	}

	sshPrivKey, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ed25519 key to ssh key: %w", err)
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return &sshPrivKey, fmt.Errorf("failed to marshal private key to PKCS#8: %w", err)
	}

	privKeyPEM := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}

	serviceConfigPathErr := StoreHostkey(pem.EncodeToMemory(privKeyPEM), serviceConfigPath, sshHostKeyFile)
	if serviceConfigPathErr != nil {
		u, err := user.Current()
		if err != nil {
			return &sshPrivKey, fmt.Errorf("failed to store the ssh hostkey file %w %w", err, serviceConfigPathErr)
		}

		err = StoreHostkey(pem.EncodeToMemory(privKeyPEM), u.HomeDir+"/.border0/", sshHostKeyFile)
		if err != nil {
			return &sshPrivKey, fmt.Errorf("failed to store the ssh hostkey file %w %w", err, serviceConfigPathErr)
		}
	}

	return &sshPrivKey, nil
}

func signer(keyFilePath string) (*ssh.Signer, error) {
	keyBytes, err := os.ReadFile(keyFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read host key %s: %w", keyFilePath, err)
	}

	privateKey, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse host key: %w", err)
	}

	return &privateKey, nil
}

func StoreHostkey(key []byte, path, filename string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", path, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check directory %s: %w", path, err)
	}
	if err := os.WriteFile(path+filename, key, 0600); err != nil {
		return fmt.Errorf("failed to write host key to %s%s: %w", path, filename, err)
	}
	return nil
}
