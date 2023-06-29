package connectorv2

import (
	"context"
)

type tokenAuth struct {
	token    string
	insecure bool
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, in ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": t.token,
		"content-type":  "application/grpc",
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return !t.insecure
}
