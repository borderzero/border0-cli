package connectorv2

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/borderzero/border0-cli/internal/api/models"
	pb "github.com/borderzero/border0-proto/connector"
	"github.com/google/uuid"
)

func (s *ConnectorService) Evaluate(ctx context.Context, socket *models.Socket, clientIP, userEmail, sessionKey string) (allowedActions []string, info map[string][]string, err error) {
	if socket == nil {
		err = fmt.Errorf("socket is nil")
		return
	}

	if clientIP == "" || userEmail == "" || sessionKey == "" {
		err = fmt.Errorf("metadata is invalid")
		return
	}

	actions, info, err := s.AuthorizeRequest(ctx, socket, clientIP, userEmail, sessionKey)
	if err != nil {
		err = fmt.Errorf("authorization request failed: %w", err)
		return
	}

	for app, actions := range actions {
		if strings.EqualFold(socket.SocketType, app) || app == "*" {
			for _, action := range actions {
				allowedActions = append(allowedActions, strings.ToLower(action))
			}
		}
	}

	return
}

func (c *ConnectorService) AuthorizeRequest(ctx context.Context, socket *models.Socket, host, userEmail, sessionKey string) (map[string][]string, map[string][]string, error) {
	requestId := uuid.New().String()

	clientIP, _, err := net.SplitHostPort(host)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse client ip: %w", err)
	}

	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_Authorize{
			Authorize: &pb.AuthorizeRequest{
				RequestId:  requestId,
				SocketId:   socket.SocketID,
				Protocol:   socket.SocketType,
				IpAddress:  clientIP,
				UserEmail:  userEmail,
				SessionKey: sessionKey,
			},
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to send authorize request: %w", err)
	}

	recChan := make(chan *pb.ControlStreamReponse)
	c.requests.Store(requestId, recChan)
	defer c.requests.Delete(requestId)
	defer close(recChan)

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case r := <-recChan:
		response := r.GetAuthorize()
		if response == nil {
			return nil, nil, fmt.Errorf("invalid response")
		}

		allowedActions := make(map[string][]string)
		for app, actions := range response.GetAllowedActions() {
			allowedActions[app] = actions.GetValues()
		}

		info := make(map[string][]string)
		for i, v := range response.GetInfo() {
			allowedActions[i] = v.GetValues()
		}

		return allowedActions, info, nil
	}
}
