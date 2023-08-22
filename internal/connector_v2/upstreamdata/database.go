package upstreamdata

import (
	"errors"
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-go/types/service"
)

func (u *UpstreamDataBuilder) buildUpstreamDataForDatabaseService(s *models.Socket, config *service.DatabaseServiceConfiguration) error {
	if config == nil {
		return fmt.Errorf("got database service with no database service configuration")
	}
	// FIXME: implement
	return errors.New("Have not implemented handling upstream data for database services")
}