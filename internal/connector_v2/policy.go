package connectorv2

import (
	"context"
	"fmt"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/connector_v2/util"
	"github.com/borderzero/border0-cli/internal/service/policy"
	pb "github.com/borderzero/border0-proto/connector"
	"github.com/google/uuid"
)

type PolicyService struct {
	connectorService *ConnectorService
}

func (c *ConnectorService) policyService() policy.PolicyService {
	return &PolicyService{
		connectorService: c,
	}
}

func (s *PolicyService) Authorize(ctx context.Context, socket *models.Socket, metadata border0.E2EEncryptionMetadata) (bool, error) {
	if socket == nil {
		return false, fmt.Errorf("socket is nil")
	}

	if metadata.ClientIP == "" || metadata.UserEmail == "" || metadata.SessionKey == "" {
		return false, fmt.Errorf("metadata is invalid")
	}

	s.connectorService.AuthorizeRequest(ctx, socket, metadata)

	return true, nil

}

func (c *ConnectorService) AuthorizeRequest(ctx context.Context, socket *models.Socket, metadata border0.E2EEncryptionMetadata) ([]string, map[string]interface{}, error) {
	requestId := uuid.New().String()

	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_Authorize{
			Authorize: &pb.AuthorizeRequest{
				RequestId:  requestId,
				SocketId:   socket.SocketID,
				Protocol:   socket.SocketType,
				IpAddress:  metadata.ClientIP,
				UserEmail:  metadata.UserEmail,
				SessionKey: metadata.SessionKey,
			},
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to send tunnel certificate sign request: %w", err)
	}

	recChan := make(chan *pb.ControlStreamReponse)
	c.requests.Store(requestId, recChan)

	defer c.requests.Delete(requestId)

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case r := <-recChan:
		response := r.GetAuthorize()
		if response == nil {
			return nil, nil, fmt.Errorf("invalid response")
		}

		if response.GetRequestId() == "" {
			return nil, nil, fmt.Errorf("invalid response")
		}

		var info map[string]interface{}
		if err := util.AsStruct(response.GetInfo(), &info); err != nil {
			return nil, nil, fmt.Errorf("failed to parse result: %w", err)
		}

		return response.GetAllowedActions(), info, nil
	}
}
