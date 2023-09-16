package policy

import (
	"context"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
)

type PolicyService interface {
	Authorize(ctx context.Context, socket *models.Socket, metadata border0.E2EEncryptionMetadata) (bool, error)
}
