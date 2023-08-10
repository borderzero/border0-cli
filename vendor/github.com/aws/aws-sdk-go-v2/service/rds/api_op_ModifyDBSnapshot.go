// Code generated by smithy-go-codegen DO NOT EDIT.

package rds

import (
	"context"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Updates a manual DB snapshot with a new engine version. The snapshot can be
// encrypted or unencrypted, but not shared or public. Amazon RDS supports
// upgrading DB snapshots for MySQL, PostgreSQL, and Oracle. This command doesn't
// apply to RDS Custom.
func (c *Client) ModifyDBSnapshot(ctx context.Context, params *ModifyDBSnapshotInput, optFns ...func(*Options)) (*ModifyDBSnapshotOutput, error) {
	if params == nil {
		params = &ModifyDBSnapshotInput{}
	}

	result, metadata, err := c.invokeOperation(ctx, "ModifyDBSnapshot", params, optFns, c.addOperationModifyDBSnapshotMiddlewares)
	if err != nil {
		return nil, err
	}

	out := result.(*ModifyDBSnapshotOutput)
	out.ResultMetadata = metadata
	return out, nil
}

type ModifyDBSnapshotInput struct {

	// The identifier of the DB snapshot to modify.
	//
	// This member is required.
	DBSnapshotIdentifier *string

	// The engine version to upgrade the DB snapshot to. The following are the
	// database engines and engine versions that are available when you upgrade a DB
	// snapshot. MySQL
	//   - 5.5.46 (supported for 5.1 DB snapshots)
	// Oracle
	//   - 19.0.0.0.ru-2022-01.rur-2022-01.r1 (supported for 12.2.0.1 DB snapshots)
	//   - 19.0.0.0.ru-2022-07.rur-2022-07.r1 (supported for 12.1.0.2 DB snapshots)
	//   - 12.1.0.2.v8 (supported for 12.1.0.1 DB snapshots)
	//   - 11.2.0.4.v12 (supported for 11.2.0.2 DB snapshots)
	//   - 11.2.0.4.v11 (supported for 11.2.0.3 DB snapshots)
	// PostgreSQL For the list of engine versions that are available for upgrading a
	// DB snapshot, see Upgrading the PostgreSQL DB Engine for Amazon RDS (https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_UpgradeDBInstance.PostgreSQL.html#USER_UpgradeDBInstance.PostgreSQL.MajorVersion)
	// .
	EngineVersion *string

	// The option group to identify with the upgraded DB snapshot. You can specify
	// this parameter when you upgrade an Oracle DB snapshot. The same option group
	// considerations apply when upgrading a DB snapshot as when upgrading a DB
	// instance. For more information, see Option group considerations (https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_UpgradeDBInstance.Oracle.html#USER_UpgradeDBInstance.Oracle.OGPG.OG)
	// in the Amazon RDS User Guide.
	OptionGroupName *string

	noSmithyDocumentSerde
}

type ModifyDBSnapshotOutput struct {

	// Contains the details of an Amazon RDS DB snapshot. This data type is used as a
	// response element in the DescribeDBSnapshots action.
	DBSnapshot *types.DBSnapshot

	// Metadata pertaining to the operation's result.
	ResultMetadata middleware.Metadata

	noSmithyDocumentSerde
}

func (c *Client) addOperationModifyDBSnapshotMiddlewares(stack *middleware.Stack, options Options) (err error) {
	err = stack.Serialize.Add(&awsAwsquery_serializeOpModifyDBSnapshot{}, middleware.After)
	if err != nil {
		return err
	}
	err = stack.Deserialize.Add(&awsAwsquery_deserializeOpModifyDBSnapshot{}, middleware.After)
	if err != nil {
		return err
	}
	if err = addSetLoggerMiddleware(stack, options); err != nil {
		return err
	}
	if err = awsmiddleware.AddClientRequestIDMiddleware(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddComputeContentLengthMiddleware(stack); err != nil {
		return err
	}
	if err = addResolveEndpointMiddleware(stack, options); err != nil {
		return err
	}
	if err = v4.AddComputePayloadSHA256Middleware(stack); err != nil {
		return err
	}
	if err = addRetryMiddlewares(stack, options); err != nil {
		return err
	}
	if err = addHTTPSignerV4Middleware(stack, options); err != nil {
		return err
	}
	if err = awsmiddleware.AddRawResponseToMetadata(stack); err != nil {
		return err
	}
	if err = awsmiddleware.AddRecordResponseTiming(stack); err != nil {
		return err
	}
	if err = addClientUserAgent(stack, options); err != nil {
		return err
	}
	if err = smithyhttp.AddErrorCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = addOpModifyDBSnapshotValidationMiddleware(stack); err != nil {
		return err
	}
	if err = stack.Initialize.Add(newServiceMetadataMiddleware_opModifyDBSnapshot(options.Region), middleware.Before); err != nil {
		return err
	}
	if err = awsmiddleware.AddRecursionDetection(stack); err != nil {
		return err
	}
	if err = addRequestIDRetrieverMiddleware(stack); err != nil {
		return err
	}
	if err = addResponseErrorMiddleware(stack); err != nil {
		return err
	}
	if err = addRequestResponseLogging(stack, options); err != nil {
		return err
	}
	return nil
}

func newServiceMetadataMiddleware_opModifyDBSnapshot(region string) *awsmiddleware.RegisterServiceMetadata {
	return &awsmiddleware.RegisterServiceMetadata{
		Region:        region,
		ServiceID:     ServiceID,
		SigningName:   "rds",
		OperationName: "ModifyDBSnapshot",
	}
}