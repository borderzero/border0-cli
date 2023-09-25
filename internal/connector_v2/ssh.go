package connectorv2

import (
	"context"
	"fmt"

	pb "github.com/borderzero/border0-proto/connector"
	"github.com/google/uuid"
)

func (c *ConnectorService) SignSshOrgCertificate(ctx context.Context, socketID, sessionKey, userEmail string, ticket, sshKey []byte) ([]byte, error) {
	requestId := uuid.New().String()
	if err := c.sendControlStreamRequest(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_SshCertificateSignRequest{
			SshCertificateSignRequest: &pb.SshCertifcateSignRequest{
				RequestId:  requestId,
				SocketId:   socketID,
				SessionKey: sessionKey,
				UserEmail:  userEmail,
				Ticket:     ticket,
				PublicKey:  sshKey,
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to send certifcate sign request: %w", err)
	}

	recChan := make(chan *pb.ControlStreamReponse)
	c.requests.Store(requestId, recChan)
	defer c.requests.Delete(requestId)
	defer close(recChan)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-recChan:
		response := r.GetSshCertificateSignResponse()
		if response == nil {
			return nil, fmt.Errorf("invalid response")
		}

		return response.GetCertificate(), nil
	}
}
