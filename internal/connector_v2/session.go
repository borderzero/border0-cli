package connectorv2

import (
	"github.com/borderzero/border0-cli/internal/api/models"
	pb "github.com/borderzero/border0-proto/connector"
	"go.uber.org/zap"
)

func (c *ConnectorService) UpdateSession(update models.SessionUpdate) error {
	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_SessionUpdate{
			SessionUpdate: &pb.SessionUpdateRequest{
				SessionKey: update.SessionKey,
				SocketId:   update.Socket.SocketID,
				UserData:   update.UserData,
			},
		},
	}); err != nil {
		c.logger.Error("failed to send session update: %s", zap.Error(err))
	}
	return nil
}
