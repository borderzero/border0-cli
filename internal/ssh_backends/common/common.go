package common

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"

	"github.com/gliderlabs/ssh"
	"go.uber.org/zap"
	gossh "golang.org/x/crypto/ssh"
)

func GetPublicKeyHandler(
	ctx context.Context,
	logger *zap.Logger,
	principal string,
	publicKey []byte,
) (ssh.PublicKeyHandler, error) {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate signer public key: %v", err)
	}

	return func(ctx ssh.Context, key ssh.PublicKey) bool {
		cert, ok := key.(*gossh.Certificate)
		if !ok {
			logger.Error("received a malformed certificate")
			return false
		}
		if !bytes.Equal(cert.SignatureKey.Marshal(), pubKey.Marshal()) {
			logger.Error("received a certificate not signed by trusted certificate authority")
			return false
		}
		var certChecker gossh.CertChecker
		if err = certChecker.CheckCert(principal, cert); err != nil {
			logger.Error("failed to check certificate principal", zap.Error(err))
			return false
		}
		return true
	}, nil
}

func GetSigners() ([]ssh.Signer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("could not generate rsa key: %s", err)
	}
	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("could not generate signer: %s", err)
	}
	return []ssh.Signer{signer}, nil
}
