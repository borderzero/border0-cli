package install

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go/aws/endpoints"

	border0 "github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
)

const (
	banner = `
-------------------------------------------------------------
Welcome to the Border0 Connector Installation Wizard for AWS!
-------------------------------------------------------------`
)

const (
	timeoutLoadDefaultConfig      = time.Second * 2
	timeoutDescribeVpcsPage       = time.Second * 10
	timeoutDescribeVpcSubnetsPage = time.Second * 10
	timeoutDescribeSsmParameters  = time.Second * 10
	timeoutCreateBorder0Token     = time.Second * 10
	timeoutPutSsmParameter        = time.Second * 10
	timeoutCreateStack            = time.Second * 10
	timeoutDescribeStackEvents    = time.Second * 10
	timeoutDescribeStacks         = time.Second * 10

	cfnStackDeployMinBackoff = time.Second * 1
	cfnStackDeployMaxBackoff = time.Second * 10
)

var (
	surveyOptionWithRocketSelectIcon = survey.WithIcons(
		func(set *survey.IconSet) { set.SelectFocus = survey.Icon{Text: "🚀"} },
	)
)

// RunWizardForAWS runs the connector cloud install wizard for AWS.
func RunWizardForAWS(ctx context.Context, cliVersion string) error {
	fmt.Println(fmt.Sprintf("%s\n", banner))

	runId := fmt.Sprintf("border0-connector-cloud-install-aws-%d", time.Now().Unix())

	loadDefaultConfigCtx, loadDefaultConfigCtxCancel := context.WithTimeout(ctx, timeoutLoadDefaultConfig)
	loadDefaultConfigCtxCancel()
	cfg, err := config.LoadDefaultConfig(loadDefaultConfigCtx)
	if err != nil {
		return fmt.Errorf("unable to load AWS SDK config: %v", err)
	}

	region, err := promptForRegion(cfg.Region)
	if err != nil {
		return fmt.Errorf("failed to prompt for AWS region: %v", err)
	}
	cfg.Region = region

	vpcId, err := promptForVpcId(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to prompt for AWS VPC ID: %v", err)
	}

	subnetId, err := promptForVpcSubnetId(ctx, cfg, vpcId)
	if err != nil {
		return fmt.Errorf("failed to prompt for AWS VPC Subnet ID: %v", err)
	}

	border0ConnectorName, err := promptForBorder0ConnectorName(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to prompt for Border0 connector name: %v", err)
	}

	border0TokenSsmParameter, err := promptForBorder0TokenSsmParameterPath(ctx, cfg, border0ConnectorName)
	if err != nil {
		return fmt.Errorf("failed to prompt for AWS SSM Parameter path for Border0 token: %v", err)
	}

	border0Connector, err := createNewBorder0Connector(ctx, border0ConnectorName, cliVersion)
	if err != nil {
		return fmt.Errorf("failed to create new Border0 connector: %v", err)
	}

	border0Token, err := generateNewBorder0ConnectorToken(ctx, border0Connector.Name, cliVersion, runId)
	if err != nil {
		return fmt.Errorf("failed to create new Border0 token: %v", err)
	}

	err = saveBorder0TokenInSsmParameterStore(
		ctx,
		cfg,
		border0Token.Token,
		border0TokenSsmParameter,
	)
	if err != nil {
		return fmt.Errorf("failed to store new Border0 token in AWS SSM: %v", err)
	}

	err = deployCloudFormationStack(
		ctx,
		cfg,
		runId,
		vpcId,
		subnetId,
		border0TokenSsmParameter,
	)
	if err != nil {
		return fmt.Errorf("failed to create connector resources in AWS: %v", err)
	}

	return nil
}

func promptForRegion(defaultRegion string) (string, error) {
	// collect list of regions from the AWS SDK
	regionChoices := []string{}
	defaultRegionChoice := ""
	for region := range endpoints.AwsPartition().Regions() {
		choiceText := region

		if region == defaultRegion {
			choiceText = fmt.Sprintf("%s (default)", choiceText)
			defaultRegionChoice = choiceText
		}

		regionChoices = append(regionChoices, choiceText)
	}

	// build question
	question := &survey.Select{
		Message: "Which AWS region would you like to install a connector in?",
		Options: regionChoices,
		Default: defaultRegionChoice,
	}

	// prompt
	var region string
	err := survey.AskOne(question, &region, surveyOptionWithRocketSelectIcon)
	if err != nil {
		return "", fmt.Errorf("failed to ask survey question: %v", err)
	}

	// success!
	return strings.Split(region, " ")[0], nil
}

func promptForVpcId(ctx context.Context, cfg aws.Config) (string, error) {
	// intialize ec2 client and VPCs paginator
	ec2Client := ec2.NewFromConfig(cfg)
	paginator := ec2.NewDescribeVpcsPaginator(ec2Client, &ec2.DescribeVpcsInput{})

	// collect list of vpcs and the default VPC from the AWS SDK
	vpcIdChoices := []string{}
	defaultVpcIdChoice := ""
	for paginator.HasMorePages() {
		describeVpcsPageCtx, describeVpcsPageCtxCancel := context.WithTimeout(ctx, timeoutDescribeVpcsPage)
		defer describeVpcsPageCtxCancel()

		output, err := paginator.NextPage(describeVpcsPageCtx)
		if err != nil {
			return "", fmt.Errorf("failed to describe VPCs via the AWS API: %v", err)
		}

		for _, vpc := range output.Vpcs {
			choiceText := fmt.Sprintf(
				"%s [%s]",
				aws.ToString(vpc.VpcId),
				aws.ToString(vpc.CidrBlock),
			)

			if aws.ToBool(vpc.IsDefault) {
				choiceText = fmt.Sprintf("%s (default)", choiceText)
				defaultVpcIdChoice = choiceText
			}

			vpcIdChoices = append(vpcIdChoices, choiceText)
		}
	}

	// build question
	question := &survey.Select{
		Message: "Which VPC would you like to install the connector in?",
		Options: vpcIdChoices,
	}
	if defaultVpcIdChoice != "" {
		question.Default = defaultVpcIdChoice
	}

	// prompt
	var vpcId string
	err := survey.AskOne(question, &vpcId, surveyOptionWithRocketSelectIcon)
	if err != nil {
		return "", fmt.Errorf("failed to ask survey question: %v", err)
	}

	// success!
	return strings.Split(vpcId, " ")[0], nil
}

func promptForVpcSubnetId(ctx context.Context, cfg aws.Config, vpcId string) (string, error) {
	// intialize ec2 client and subnets paginator
	ec2Client := ec2.NewFromConfig(cfg)
	paginator := ec2.NewDescribeSubnetsPaginator(ec2Client, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcId}}},
	})

	// collect list of subnets in the vpc
	subnetIdChoices := []string{}
	for paginator.HasMorePages() {
		describeSubnetsPageCtx, describeSubnetsPageCtxCancel := context.WithTimeout(ctx, timeoutDescribeVpcSubnetsPage)
		defer describeSubnetsPageCtxCancel()

		output, err := paginator.NextPage(describeSubnetsPageCtx)
		if err != nil {
			return "", fmt.Errorf("failed to describe VPC subnets via the AWS API: %v", err)
		}

		for _, subnet := range output.Subnets {
			choiceText := fmt.Sprintf(
				"%s [%s %s]",
				aws.ToString(subnet.SubnetId),
				aws.ToString(subnet.AvailabilityZone),
				aws.ToString(subnet.CidrBlock),
			)
			subnetIdChoices = append(subnetIdChoices, choiceText)
		}
	}

	// build question
	question := &survey.Select{
		Message: "Which VPC subnet would you like to install the connector in?",
		Options: subnetIdChoices,
	}

	// prompt
	var subnetIdChoice string
	err := survey.AskOne(question, &subnetIdChoice, surveyOptionWithRocketSelectIcon)
	if err != nil {
		return "", fmt.Errorf("failed to ask survey question: %v", err)
	}

	// success!
	return strings.Split(subnetIdChoice, " ")[0], nil
}

func promptForBorder0ConnectorName(ctx context.Context, cfg aws.Config) (string, error) {
	var connectorNameTarget string
	err := survey.AskOne(
		&survey.Input{
			Message: "What name would you like for your new Border0 Connector?",
			Default: fmt.Sprintf("my-connector-%d", time.Now().Unix()),
		},
		&connectorNameTarget,
		survey.WithValidator(survey.Required),
		// TODO: validator for connector not already existing
		// TODO: validator for connector name regex
	)
	if err != nil {
		return "", fmt.Errorf("failed to ask survey question: %v", err)
	}
	return connectorNameTarget, nil
}

func promptForBorder0TokenSsmParameterPath(
	ctx context.Context,
	cfg aws.Config,
	connectorName string,
) (string, error) {
	var parameterPathTarget string
	err := survey.AskOne(
		&survey.Input{
			Message: "What is the SSM parameter store path you'd like your border0 token to be stored at?",
			Default: fmt.Sprintf("connector-%s-token", connectorName),
		},
		&parameterPathTarget,
		survey.WithValidator(survey.Required),
		survey.WithValidator(getBorder0TokenSsmParameterPathValidator(ctx, cfg)),
	)
	if err != nil {
		return "", fmt.Errorf("failed to ask survey question: %v", err)
	}
	return parameterPathTarget, nil
}

// returns a survey.Validator that checks that a given SSM parameter does not already exist.
func getBorder0TokenSsmParameterPathValidator(ctx context.Context, cfg aws.Config) survey.Validator {
	ssmClient := ssm.NewFromConfig(cfg)

	return func(userInput interface{}) error {
		// cast to string
		ssmParameterPath, ok := userInput.(string)
		if !ok {
			return fmt.Errorf("user input not a string")
		}

		// try lookup
		describeSsmParametersCtx, describeSsmParametersCtxCancel := context.WithTimeout(ctx, timeoutDescribeSsmParameters)
		defer describeSsmParametersCtxCancel()
		describeParametersOutput, err := ssmClient.DescribeParameters(describeSsmParametersCtx, &ssm.DescribeParametersInput{
			Filters: []ssmtypes.ParametersFilter{{Key: ssmtypes.ParametersFilterKeyName, Values: []string{ssmParameterPath}}},
		})

		if err != nil {
			return fmt.Errorf("failed to describe SSM parameters via the AWS API: %v", err)
		}

		// fail if exists
		if len(describeParametersOutput.Parameters) > 0 {
			return fmt.Errorf("parameter \"%s\" already exists in %s", ssmParameterPath, cfg.Region)
		}

		// success!
		return nil
	}
}

func createNewBorder0Connector(
	ctx context.Context,
	connectorName string,
	cliVersion string,
) (*models.Connector, error) {
	border0Client := border0.NewAPI(border0.WithVersion(cliVersion))

	connector, err := border0Client.CreateConnector(ctx, connectorName, fmt.Sprintf("AWS Cloud-Install Border0 Connector"))
	if err != nil {
		return nil, fmt.Errorf("failed to create a new Border0 connector via the Border0 API: %v", err)
	}

	fmt.Printf("🚀 Border0 connector \"%s\" created successfully!\n", connectorName)

	return connector, nil
}

func generateNewBorder0ConnectorToken(
	ctx context.Context,
	connectorName string,
	cliVersion string,
	tokenName string,
) (*models.ConnectorToken, error) {
	border0Client := border0.NewAPI(border0.WithVersion(cliVersion))

	oneYearFromNow := time.Now().AddDate(1, 0, 0)

	createTokenCtx, createTokenCtxCancel := context.WithTimeout(ctx, timeoutCreateBorder0Token)
	defer createTokenCtxCancel()

	token, err := border0Client.CreateConnectorToken(
		createTokenCtx,
		connectorName,
		fmt.Sprintf("connector-token-%d", time.Now().Unix()),
		&oneYearFromNow,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a new Border0 token for connector via the Border0 API: %v", err)
	}

	fmt.Printf("🚀 Border0 connector token \"%s\" created successfully!\n", token.Name)

	return token, nil
}

func saveBorder0TokenInSsmParameterStore(
	ctx context.Context,
	cfg aws.Config,
	border0Token string,
	border0TokenSsmParameterPath string,
) error {
	ssmClient := ssm.NewFromConfig(cfg)

	putSsmParameterCtx, putSsmParameterCtxCancel := context.WithTimeout(ctx, timeoutPutSsmParameter)
	defer putSsmParameterCtxCancel()

	_, err := ssmClient.PutParameter(putSsmParameterCtx, &ssm.PutParameterInput{
		Name:  &border0TokenSsmParameterPath,
		Value: &border0Token,
		Type:  ssmtypes.ParameterTypeSecureString,
	})
	if err != nil {
		return fmt.Errorf("failed to put parameter via the AWS API: %v", err)
	}

	fmt.Printf("🚀 SSM Parameter \"%s\" created successfully!\n", border0TokenSsmParameterPath)
	return nil
}

// Improvements:
//   - handle graceful cancellation
//   - call context cancel functions within a different
//     function to avoid acumulation of deferred statements
func deployCloudFormationStack(
	ctx context.Context,
	cfg aws.Config,
	stackName string,
	vpcId string,
	subnetId string,
	border0TokenSsmParameter string,
) error {
	cfnClient := cloudformation.NewFromConfig(cfg)

	createStackInput := &cloudformation.CreateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(awsCloudFormationTemplateBody),
		Parameters: []cfntypes.Parameter{
			{
				ParameterKey:   aws.String("VpcId"),
				ParameterValue: aws.String(vpcId),
			},
			{
				ParameterKey:   aws.String("SubnetId"),
				ParameterValue: aws.String(subnetId),
			},
			{
				ParameterKey:   aws.String("Border0TokenSsmParameter"),
				ParameterValue: aws.String(border0TokenSsmParameter),
			},
		},
		Capabilities: []cfntypes.Capability{
			cfntypes.CapabilityCapabilityIam,
		},
	}

	createStackCtx, createStackCtxCancel := context.WithTimeout(ctx, timeoutCreateStack)
	defer createStackCtxCancel()

	_, err := cfnClient.CreateStack(createStackCtx, createStackInput)
	if err != nil {
		return fmt.Errorf("failed to create CloudFormation stack via the AWS API: %v", err)
	}

	fmt.Println(fmt.Sprintf("🚀 CloudFormation stack \"%s\" creation initiated, events below:", stackName))

	// poll stack events
	eventsSeen := make(map[string]struct{})
	backoff := cfnStackDeployMinBackoff
	for {

		// get stack status via aws api... note that we do this before the describe stack events call
		// even though we only use the result after processing describe stack events. This is deliberate
		// in order to avoid the possibility of missing events in the console output. Without doing this
		// it is possible that the stack completes between the describe stack events call and the describe
		// stacks call, meaning this function will exit without describing (and printing) the last few events.
		describeStacksCtx, describeStacksCtxCancel := context.WithTimeout(ctx, timeoutDescribeStacks)
		defer describeStacksCtxCancel()

		stackOutput, err := cfnClient.DescribeStacks(
			describeStacksCtx,
			&cloudformation.DescribeStacksInput{StackName: aws.String(stackName)},
		)
		if err != nil {
			return fmt.Errorf("failed to describe CloudFormation stack via the AWS API: %v", err)
		}
		if len(stackOutput.Stacks) < 1 {
			return fmt.Errorf("failed to describe CloudFormation stack via the AWS API: stack not found")
		}

		// get stack events via aws api
		describeStackEventsCtx, describeStackEventsCtxCancel := context.WithTimeout(ctx, timeoutDescribeStackEvents)
		defer describeStackEventsCtxCancel()

		eventsOutput, err := cfnClient.DescribeStackEvents(
			describeStackEventsCtx,
			&cloudformation.DescribeStackEventsInput{StackName: aws.String(stackName)},
		)
		if err != nil {
			return fmt.Errorf("failed to describe CloudFormation stack events via the AWS API: %v", err)
		}

		// collect events not already seen
		newEvents := []cfntypes.StackEvent{}
		for _, event := range eventsOutput.StackEvents {
			if _, seen := eventsSeen[aws.ToString(event.EventId)]; !seen {
				// mark event as seen
				eventsSeen[aws.ToString(event.EventId)] = struct{}{}

				// add to new events slice
				newEvents = append(newEvents, event)
			}
		}

		// sort events not seen from oldest to newest
		sort.SliceStable(newEvents, func(i, j int) bool {
			return aws.ToTime(newEvents[i].Timestamp).Before(aws.ToTime(newEvents[j].Timestamp))
		})

		// print new events info to stdout
		for _, event := range newEvents {
			eventMessage := fmt.Sprintf(
				"    %s Resource: %s %s, Status: %s",
				getCfnResourceStatusEmoji(event.ResourceStatus),
				aws.ToString(event.ResourceType),
				aws.ToString(event.LogicalResourceId),
				string(event.ResourceStatus),
			)
			if aws.ToString(event.ResourceStatusReason) != "" {
				eventMessage = fmt.Sprintf(
					"%s, Reason: %s",
					eventMessage,
					aws.ToString(event.ResourceStatusReason),
				)
			}
			fmt.Println(eventMessage)
		}

		// handle terminal/final stack statuses
		stackStatus := stackOutput.Stacks[0].StackStatus
		if stackStatus == cfntypes.StackStatusCreateComplete {
			fmt.Printf("🚀 CloudFormation stack %s created successfully!\n", stackName)
			return nil
		}
		if stackStatus == cfntypes.StackStatusCreateFailed ||
			stackStatus == cfntypes.StackStatusRollbackComplete ||
			stackStatus == cfntypes.StackStatusRollbackFailed {
			return fmt.Errorf("CloudFormation stack creation finished with status %s", stackStatus)
		}

		// wait before next poll
		time.Sleep(backoff)

		// increase backoff time (up to a maximum) or reset it if there was a new event
		if len(newEvents) > 0 {
			backoff = cfnStackDeployMinBackoff
		} else {
			if backoff < cfnStackDeployMaxBackoff {
				backoff *= 2
				if backoff > cfnStackDeployMaxBackoff {
					backoff = cfnStackDeployMaxBackoff
				}
			}
		}
	}
}

func getCfnResourceStatusEmoji(eventStatus cfntypes.ResourceStatus) string {
	switch eventStatus {
	case
		cfntypes.ResourceStatusCreateInProgress,
		cfntypes.ResourceStatusDeleteInProgress,
		cfntypes.ResourceStatusUpdateInProgress,
		cfntypes.ResourceStatusUpdateRollbackInProgress,
		cfntypes.ResourceStatusImportInProgress,
		cfntypes.ResourceStatusImportRollbackInProgress,
		cfntypes.ResourceStatusRollbackInProgress:
		return "⌛"
	case
		cfntypes.ResourceStatusCreateFailed,
		cfntypes.ResourceStatusDeleteFailed,
		cfntypes.ResourceStatusRollbackFailed,
		cfntypes.ResourceStatusUpdateFailed,
		cfntypes.ResourceStatusUpdateRollbackFailed,
		cfntypes.ResourceStatusImportFailed,
		cfntypes.ResourceStatusImportRollbackFailed:
		return "❌"
	case
		cfntypes.ResourceStatusCreateComplete,
		cfntypes.ResourceStatusDeleteComplete,
		cfntypes.ResourceStatusRollbackComplete,
		cfntypes.ResourceStatusImportComplete,
		cfntypes.ResourceStatusImportRollbackComplete,
		cfntypes.ResourceStatusUpdateComplete,
		cfntypes.ResourceStatusUpdateRollbackComplete:
		return "✅"
	case
		cfntypes.ResourceStatusDeleteSkipped:
		return "⏭️"
	default:
		return "❓"
	}
}
