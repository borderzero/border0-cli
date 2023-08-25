package cloudsql

import (
	"context"
	"fmt"
	"log"
	"net"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/borderzero/border0-cli/internal/border0"
)

func Serve(l net.Listener, instanceName, credentialsFile string, credentialsJSON []byte, withIAM bool) error {
	ctx := context.Background()
	dialer, err := NewDialer(ctx, instanceName, credentialsFile, credentialsJSON, withIAM)
	if err != nil {
		return fmt.Errorf("failed to create dialer for cloudSQL: %s", err)
	}

	for {
		rconn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept connection: %s", err)
		}

		go func() {
			lconn, err := dialer.Dial(ctx, instanceName)
			if err != nil {
				log.Printf("failed to connect to cloudSQL: %s", err)
				return
			}

			border0.ProxyConnection(rconn, lconn)
		}()
	}
}

func NewDialer(ctx context.Context, instanceName, credentialsFile string, credentialsJSON []byte, withIAM bool) (*cloudsqlconn.Dialer, error) {
	var opts []cloudsqlconn.Option
	if credentialsFile != "" {
		opts = append(opts, cloudsqlconn.WithCredentialsFile(credentialsFile))
	}
	if len(credentialsJSON) > 0 {
		opts = append(opts, cloudsqlconn.WithCredentialsJSON(credentialsJSON))
	}

	if withIAM {
		opts = append(opts, cloudsqlconn.WithIAMAuthN())
	}

	return cloudsqlconn.NewDialer(ctx, opts...)
}
