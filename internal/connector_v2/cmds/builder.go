package cmds

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/borderzero/border0-go/service/connector/types"
)

// MetadataFromContext gathers all metadata
// regarding where/how the connector is running.
func MetadataFromContext(ctx context.Context) *types.ConnectorMetadata {
	metadata := &types.ConnectorMetadata{}

	trySetAwsEc2IdentityMetadata(ctx, metadata)

	return metadata
}

func trySetAwsEc2IdentityMetadata(ctx context.Context, cmd *types.ConnectorMetadata) {
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
	cmd.AwsEc2IdentityMetadata = &types.AwsEc2IdentityMetadata{
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