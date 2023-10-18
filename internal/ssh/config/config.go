package config

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-go/types/common"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type ProxyConfig struct {
	Username           string
	Password           string
	IdentityFile       string
	IdentityPrivateKey []byte
	Hostname           string
	Port               int
	SshClientConfig    *ssh.ClientConfig
	SshServerConfig    *ssh.ServerConfig
	AwsSSMTarget       string
	AwsEC2InstanceId   string
	AWSRegion          string
	AWSProfile         string
	ECSSSMProxy        *ECSSSMProxy
	AwsConfig          aws.Config
	AwsUpstreamType    string
	Logger             *zap.Logger
	AwsCredentials     *common.AwsCredentials
	Recording          bool
	EndToEndEncryption bool
	Hostkey            *ssh.Signer
	OrgSshCA           ssh.PublicKey
	Socket             *models.Socket
	Border0API         border0.Border0API
	Border0CertAuth    bool
}

type ECSSSMProxy struct {
	Cluster    string
	Services   []string
	Tasks      []string
	Containers []string
}

func BuildProxyConfig(logger *zap.Logger, socket models.Socket, AWSRegion, AWSProfile string, hostkey *ssh.Signer, org *models.Organization, border0API border0.Border0API) (*ProxyConfig, error) {
	if socket.ConnectorLocalData == nil && !socket.EndToEndEncryptionEnabled {
		return nil, nil
	}

	isNormalSSHSocket := socket.UpstreamType != "aws-ssm" && socket.UpstreamType != "aws-ec2connect" && !socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled && !socket.EndToEndEncryptionEnabled
	if isNormalSSHSocket {
		// For connector v2 sockets CAN have an upsream username set
		// so the check below would not pass - so we add this new one.
		if socket.IsBorder0Certificate && !socket.EndToEndEncryptionEnabled {
			return nil, nil
		}
		if socket.ConnectorLocalData.UpstreamUsername == "" && socket.ConnectorLocalData.UpstreamPassword == "" {
			if len(socket.ConnectorLocalData.UpstreamIdentityPrivateKey) == 0 && socket.ConnectorLocalData.UpstreamIdentifyFile == "" {
				return nil, nil
			}
		}
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster == "" && socket.ConnectorLocalData.AwsEC2InstanceId == "" {
		return nil, fmt.Errorf("aws_ecs_cluster or aws ec2 instance id is required for aws-ssm upstream type")
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster != "" && socket.ConnectorLocalData.AwsEC2InstanceId != "" {
		return nil, fmt.Errorf("aws_ecs_cluster and aws ec2 instance id are mutually exclusive")
	}

	if socket.UpstreamType != "aws-ssm" && !socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled && (socket.ConnectorLocalData.AWSECSCluster != "" || socket.ConnectorLocalData.AwsEC2InstanceId != "") {
		return nil, fmt.Errorf("aws_ecs_cluster or aws ec2 instance id is defined but upstream_type is not aws-ssm")
	}

	if socket.UpstreamType == "aws-ec2connect" || socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled {
		if socket.ConnectorLocalData.AwsEC2InstanceId == "" {
			return nil, fmt.Errorf("aws ec2 instance id is required for aws-ec2connect upstream type")
		}
	}

	proxyConfig := &ProxyConfig{
		Logger:             logger,
		Hostname:           socket.ConnectorData.TargetHostname,
		Port:               socket.ConnectorData.Port,
		Username:           socket.ConnectorLocalData.UpstreamUsername,
		Password:           socket.ConnectorLocalData.UpstreamPassword,
		IdentityFile:       socket.ConnectorLocalData.UpstreamIdentifyFile,
		IdentityPrivateKey: socket.ConnectorLocalData.UpstreamIdentityPrivateKey,
		AwsEC2InstanceId:   socket.ConnectorLocalData.AwsEC2InstanceId,
		AwsSSMTarget:       socket.ConnectorLocalData.AwsEC2InstanceId, // when instance id empty and ecs cluster is given, target will be constructed during connection
		AWSRegion:          AWSRegion,
		AWSProfile:         AWSProfile,
		AwsCredentials:     socket.ConnectorLocalData.AwsCredentials,
		Recording:          socket.RecordingEnabled,
		EndToEndEncryption: socket.EndToEndEncryptionEnabled,
		Socket:             &socket,
		Border0API:         border0API,
	}

	switch {
	case socket.UpstreamType == "aws-ssm":
		proxyConfig.AwsUpstreamType = "aws-ssm"
	case socket.UpstreamType == "aws-ec2connect" || socket.ConnectorLocalData.AWSEC2InstanceConnectEnabled:
		proxyConfig.AwsUpstreamType = "aws-ec2connect"
	}

	if socket.UpstreamType == "aws-ssm" && socket.ConnectorLocalData.AWSECSCluster != "" {
		// TODO: when ECSSSMProxy is not nil, proxyConfig.AwsSSMTarget will be constructed
		// from ecs cluster during connection... this kind of sucks... improve
		proxyConfig.ECSSSMProxy = &ECSSSMProxy{
			Cluster:    socket.ConnectorLocalData.AWSECSCluster,
			Services:   socket.ConnectorLocalData.AWSECSServices,
			Tasks:      socket.ConnectorLocalData.AWSECSTasks,
			Containers: socket.ConnectorLocalData.AWSECSContainers,
		}
	}

	if socket.EndToEndEncryptionEnabled {
		proxyConfig.Hostkey = hostkey
		if orgSshCA, ok := org.Certificates["ssh_public_key"]; ok {
			orgCa, _, _, _, err := ssh.ParseAuthorizedKey([]byte(orgSshCA))
			if err != nil {
				return nil, err
			}

			proxyConfig.OrgSshCA = orgCa
		}
	}

	return proxyConfig, nil
}

func (c *ProxyConfig) IsRecordingEnabled() bool {
	return c.Recording && c.EndToEndEncryption
}
