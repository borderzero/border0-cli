package cmds

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/borderzero/border0-cli/internal"
	"github.com/borderzero/border0-go/types/connector"
	"github.com/shirou/gopsutil/v3/host"
)

// MetadataFromContext gathers all metadata
// regarding where/how the connector is running.
func MetadataFromContext(ctx context.Context) *connector.Metadata {
	metadata := &connector.Metadata{}

	trySetAwsEc2IdentityMetadata(ctx, metadata)
	trySetConnectorInternalMetadata(ctx, metadata)

	return metadata
}

func trySetAwsEc2IdentityMetadata(ctx context.Context, cmd *connector.Metadata) {
	awsConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		// ignored error
		return
	}
	identityDoc, err := imds.NewFromConfig(awsConfig).GetInstanceIdentityDocument(ctx, &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		// ignored error
		return
	}
	cmd.AwsEc2IdentityMetadata = &connector.AwsEc2IdentityMetadata{
		AwsAccountId:        identityDoc.AccountID,
		AwsRegion:           identityDoc.Region,
		AwsAvailabilityZone: identityDoc.AvailabilityZone,
		Ec2InstanceId:       identityDoc.InstanceID,
		Ec2InstanceType:     identityDoc.InstanceType,
		Ec2ImageId:          identityDoc.ImageID,
		KernelId:            identityDoc.KernelID,
		RamdiskId:           identityDoc.RamdiskID,
		Architecture:        identityDoc.Architecture,
		PrivateIpAddress:    identityDoc.PrivateIP,
	}
	return
}

func trySetConnectorInternalMetadata(ctx context.Context, cmd *connector.Metadata) {
	var hostMetadata connector.HostMetadata

	info, _ := host.InfoWithContext(ctx) // ignored error
	if info != nil {
		hostMetadata = connector.HostMetadata{
			Hostname:        info.Hostname,
			Uptime:          info.Uptime,
			OS:              info.OS,
			Platform:        info.Platform,
			PlatformVersion: info.PlatformVersion,
			KernelVersion:   info.KernelVersion,
			KernelArch:      info.KernelArch,
		}
	}

	cmd.ConnectorInternalMetadata = &connector.ConnectorInternalMetadata{
		Version:      internal.Version,
		BuiltDate:    internal.Date,
		HostMetadata: &hostMetadata,
	}
}
