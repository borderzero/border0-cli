// Code generated by smithy-go-codegen DO NOT EDIT.

package cloudformation

import (
	"context"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Publishes the specified extension to the CloudFormation registry as a public
// extension in this Region. Public extensions are available for use by all
// CloudFormation users. For more information about publishing extensions, see
// Publishing extensions to make them available for public use (https://docs.aws.amazon.com/cloudformation-cli/latest/userguide/publish-extension.html)
// in the CloudFormation CLI User Guide. To publish an extension, you must be
// registered as a publisher with CloudFormation. For more information, see
// RegisterPublisher (https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RegisterPublisher.html)
// .
func (c *Client) PublishType(ctx context.Context, params *PublishTypeInput, optFns ...func(*Options)) (*PublishTypeOutput, error) {
	if params == nil {
		params = &PublishTypeInput{}
	}

	result, metadata, err := c.invokeOperation(ctx, "PublishType", params, optFns, c.addOperationPublishTypeMiddlewares)
	if err != nil {
		return nil, err
	}

	out := result.(*PublishTypeOutput)
	out.ResultMetadata = metadata
	return out, nil
}

type PublishTypeInput struct {

	// The Amazon Resource Name (ARN) of the extension. Conditional: You must specify
	// Arn , or TypeName and Type .
	Arn *string

	// The version number to assign to this version of the extension. Use the
	// following format, and adhere to semantic versioning when assigning a version
	// number to your extension: MAJOR.MINOR.PATCH For more information, see Semantic
	// Versioning 2.0.0 (https://semver.org/) . If you don't specify a version number,
	// CloudFormation increments the version number by one minor version release. You
	// cannot specify a version number the first time you publish a type.
	// CloudFormation automatically sets the first version number to be 1.0.0 .
	PublicVersionNumber *string

	// The type of the extension. Conditional: You must specify Arn , or TypeName and
	// Type .
	Type types.ThirdPartyType

	// The name of the extension. Conditional: You must specify Arn , or TypeName and
	// Type .
	TypeName *string

	noSmithyDocumentSerde
}

type PublishTypeOutput struct {

	// The Amazon Resource Name (ARN) assigned to the public extension upon
	// publication.
	PublicTypeArn *string

	// Metadata pertaining to the operation's result.
	ResultMetadata middleware.Metadata

	noSmithyDocumentSerde
}

func (c *Client) addOperationPublishTypeMiddlewares(stack *middleware.Stack, options Options) (err error) {
	err = stack.Serialize.Add(&awsAwsquery_serializeOpPublishType{}, middleware.After)
	if err != nil {
		return err
	}
	err = stack.Deserialize.Add(&awsAwsquery_deserializeOpPublishType{}, middleware.After)
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
	if err = addClientUserAgent(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddErrorCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = smithyhttp.AddCloseResponseBodyMiddleware(stack); err != nil {
		return err
	}
	if err = stack.Initialize.Add(newServiceMetadataMiddleware_opPublishType(options.Region), middleware.Before); err != nil {
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

func newServiceMetadataMiddleware_opPublishType(region string) *awsmiddleware.RegisterServiceMetadata {
	return &awsmiddleware.RegisterServiceMetadata{
		Region:        region,
		ServiceID:     ServiceID,
		SigningName:   "cloudformation",
		OperationName: "PublishType",
	}
}