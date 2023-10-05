package install

// FIXME: this will go in S3... the source of truth is in
// https://github.com/borderzero/examples/blob/main/cloudformation-templates/aws_connector_installer/template.yaml
const awsCloudFormationTemplateBody = `
AWSTemplateFormatVersion: '2010-09-09'

####################################
##           PARAMETERS           ##
####################################
Parameters:

  VpcId:
    Type: AWS::EC2::VPC::Id
    Description: The ID of the VPC where to run the connector

  SubnetId:
    Type: AWS::EC2::Subnet::Id
    Description: The ID of the Subnet where to run the connector

  InstanceType:
    Type: String
    Description: The EC2 Instance Type for the connector instance
    Default: t4g.nano

  Border0TokenSsmParameter:
    Type: AWS::SSM::Parameter::Name
    Description: The name/path of the SSM parameter for the Border0 token (which the connector instance uses to authenticate against your Border0 organization)

####################################
##           CONDITIONS           ##
####################################
Conditions:

  Border0TokenSsmParameterStartsWithSlash: !Equals [ !Select [ 0, !Split [ "/", !Ref Border0TokenSsmParameter ] ], "" ]

####################################
##           RESOURCES            ##
####################################
Resources:

  ConnectorInstanceRole:
    Type: 'AWS::IAM::Role'
    Properties:
      AssumeRolePolicyDocument:
        Statement:
          - Effect: Allow
            Principal:
              Service:
                - ec2.amazonaws.com
            Action:
              - 'sts:AssumeRole'
      ManagedPolicyArns:
        - 'arn:aws:iam::aws:policy/AmazonEC2ReadOnlyAccess'
        - 'arn:aws:iam::aws:policy/AmazonRDSReadOnlyAccess'
        - 'arn:aws:iam::aws:policy/AmazonSSMManagedEC2InstanceDefaultPolicy' # allows SSM access to connector instances as breakglass
      Policies:
        # ECS ReadOnly Policy for ECS discovery and providing Border0 clients
        # with the menu with all available tasks and containers in ecs service.
        - PolicyName: AmazonECSReadOnlyAccess
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action:
                  - 'ecs:DescribeClusters'
                  - 'ecs:DescribeContainerInstances'
                  - 'ecs:DescribeServices'
                  - 'ecs:DescribeTaskDefinition'
                  - 'ecs:DescribeTasks'
                  - 'ecs:ListClusters'
                  - 'ecs:ListContainerInstances'
                  - 'ecs:ListServices'
                  - 'ecs:ListTaskDefinitionFamilies'
                  - 'ecs:ListTaskDefinitions'
                  - 'ecs:ListTasks'
                Resource: '*'
        # SSM Parameter ReadOnly access to the SSM parameter of the connector's token.
        - PolicyName: AccessToBorder0TokenSsmParameter
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action: 'ssm:DescribeParameters'
                Resource: !Sub 'arn:aws:ssm:${AWS::Region}:${AWS::AccountId}:parameter/*'
              - Effect: Allow
                Action:
                  - 'ssm:GetParameter'
                  - 'ssm:GetParameters'
                Resource:
                  # SSM parameters can begin with a slash character '/'. When they begin
                  # with a slash character, it must be ommitted from the resource ARN here.
                  Fn::Sub:
                    - arn:aws:ssm:${AWS::Region}:${AWS::AccountId}:parameter${SsmParameterPath}
                    - SsmParameterPath:
                        !If [Border0TokenSsmParameterStartsWithSlash, !Sub "${Border0TokenSsmParameter}", !Sub "/${Border0TokenSsmParameter}"]
        # Allow generating temporary user credentials for database IAM access.
        - PolicyName: GenerateTemporaryDatabaseCredsForRds
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action: 'rds-db:connect'
                Resource: !Sub 'arn:aws:rds-db:*:${AWS::AccountId}:dbuser:*'
        # Allow sending public keys to any ec2 instance (for ec2 instance connect).
        - PolicyName: SendSshPublicKeysToEc2Instances
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action: 'ec2-instance-connect:SendSSHPublicKey'
                Resource: !Sub 'arn:aws:ec2:*:${AWS::AccountId}:instance/*'
        # Allow starting and terminating ssm sessions to any instance (for ssm)
        - PolicyName: StartAndTerminateSsmSessions
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action: 'ssm:StartSession'
                Resource:
                  - !Sub 'arn:aws:ecs:*:${AWS::AccountId}:task/*'
                  - !Sub 'arn:aws:ec2:*:${AWS::AccountId}:instance/*'
                  - !Sub 'arn:aws:ssm:*:${AWS::AccountId}:document/AWS-StartSSHSession'
              - Effect: Allow
                Action: 'ssm:TerminateSession'
                Resource:
                  - !Sub 'arn:aws:ssm:*:${AWS::AccountId}:session/*'
        # Allow the EC2 discoverer to check EC2 instances' SSM status (e.g. registered with ssm or not)
        - PolicyName: DescribeInstancesSsmStatus
          PolicyDocument:
            Version: '2012-10-17'
            Statement:
              - Effect: Allow
                Action: 'ssm:DescribeInstanceInformation'
                Resource: !Sub 'arn:aws:ssm:*:${AWS::AccountId}:*'

  ConnectorInstanceSecurityGroup:
    Type: 'AWS::EC2::SecurityGroup'
    Properties:
      GroupDescription: Security Group for instance with only egress allowed
      VpcId: !Ref VpcId
      SecurityGroupEgress:
        - CidrIp: 0.0.0.0/0
          IpProtocol: -1

  ConnectorInstanceProfile:
    Type: 'AWS::IAM::InstanceProfile'
    Properties:
      Roles:
        - !Ref ConnectorInstanceRole

  ConnectorInstanceLaunchTemplate:
    Type: 'AWS::EC2::LaunchTemplate'
    Properties:
      LaunchTemplateName: !Sub ${AWS::StackName}-launch-template
      LaunchTemplateData:
        IamInstanceProfile:
          Arn: !GetAtt ConnectorInstanceProfile.Arn
        ImageId: '{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64}}'
        InstanceType: !Ref InstanceType
        NetworkInterfaces:
          - DeviceIndex: 0
            AssociatePublicIpAddress: true
            DeleteOnTermination: true
            Groups:
            - !Ref ConnectorInstanceSecurityGroup
        UserData:
          Fn::Base64:
            !Sub |
              #!/bin/bash -xe
              sudo curl https://download.border0.com/linux_arm64/border0 -o /usr/local/bin/border0
              sudo chmod +x /usr/local/bin/border0
              sudo BORDER0_API=https://api.staging.border0.com/api/v1 BORDER0_API_URL=https://api.staging.border0.com/api/v1/ BORDER0_TUNNEL=tunnel.staging.border0.com BORDER0_CONNECTOR_SERVER=capi.staging.border0.com:443 BORDER0_TOKEN=from:aws:ssm:${Border0TokenSsmParameter} border0 connector start

  ConnectorInstanceAutoScalingGroup:
    Type: 'AWS::AutoScaling::AutoScalingGroup'
    Properties:
      MinSize: '1'
      MaxSize: '1'
      DesiredCapacity: '1'
      Tags:
        - Key: Name
          Value: Border0-Connector
          PropagateAtLaunch: true
      LaunchTemplate:
        LaunchTemplateId: !Ref ConnectorInstanceLaunchTemplate
        Version: !GetAtt ConnectorInstanceLaunchTemplate.LatestVersionNumber
      VPCZoneIdentifier:
        - !Ref SubnetId
      MetricsCollection:
        - Granularity: '1Minute'
          Metrics:
            - GroupMinSize
            - GroupMaxSize
            - GroupDesiredCapacity
            - GroupInServiceInstances
            - GroupPendingInstances
            - GroupStandbyInstances
            - GroupTerminatingInstances
            - GroupTotalInstances

`
