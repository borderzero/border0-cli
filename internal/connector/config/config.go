package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/aws/aws-sdk-go/service/ssm/ssmiface"
	"github.com/spf13/viper"
)

var ErrInvalidConnectorName = errors.New("invalid connector name")

type SocketConfig struct {
	Host                           string
	Port                           int
	Name                           string
	Type                           string
	Description                    string
	AllowedEmailAddresses          []string `mapstructure:"allowed_email_addresses"`
	AllowedEmailDomains            []string `mapstructure:"allowed_email_domains"`
	UpstreamUserDeprecated         string   `mapstructure:"upstream_user"`
	UpstreamUser                   string   `mapstructure:"upstream_username"`
	UpstreamPassword               string   `mapstructure:"upstream_password"`
	UpstreamType                   string   `mapstructure:"upstream_type"`
	DatabaseCredentials            string   `mapstructure:"database_credentials"`
	UpstreamHttpHostname           string   `mapstructure:"upstream_http_hostname"`
	ConnectorAuthenticationEnabled bool     `mapstructure:"connector_authentication"`
	Policies                       []string `mapstructure:"policies"`

	UpstreamCertFile      string   `mapstructure:"upstream_certificate_file"`
	UpstreamKeyFile       string   `mapstructure:"upstream_key_file"`
	UpstreamCACertFile    string   `mapstructure:"upstream_ca_file"`
	UpstreamIdentifyFile  string   `mapstructure:"upstream_identity_file"`
	UpstreamTLS           *bool    `mapstructure:"upstream_tls,omitempty"`
	RdsIAMAuth            bool     `mapstructure:"rds_iam_auth"`
	AWSRegion             string   `mapstructure:"aws_region"`
	AWSEC2Target          string   `mapstructure:"aws_ec2_target"`
	AWSEC2AZ              string   `mapstructure:"aws_ec2_az"`
	AWSEC2ConnectEnabled  bool     `mapstructure:"aws_ec2_connect"`
	CloudSQLConnector     bool     `mapstructure:"cloudsql_connector"`
	CloudSQLIAMAuth       bool     `mapstructure:"cloudsql_iam_auth"`
	CloudSQLInstance      string   `mapstructure:"cloudsql_instance"`
	GoogleCredentialsFile string   `mapstructure:"google_credentials_file"`
	SSHServer             bool     `mapstructure:"sshserver"`
	AWSECSCluster         string   `mapstructure:"aws_ecs_cluster"`
	TaskFilter            []string `mapstructure:"aws_ecs_tasks"`
	ServiceFilter         []string `mapstructure:"aws_ecs_services"`
	ContainerFilter       []string `mapstructure:"aws_ecs_containers"`
}

type Credentials struct {
	Username string
	User     string
	Password string
	Token    string
}

func (c Credentials) GetUsername() string {
	if c.Username != "" {
		return c.Username
	}

	if c.User != "" {
		return c.User
	}

	return ""
}

type ConnectorGroups struct {
	Group                          string
	AllowedEmailAddresses          []string `mapstructure:"allowed_email_addresses"`
	AllowedEmailDomains            []string `mapstructure:"allowed_email_domains"`
	ConnectorAuthenticationEnabled bool     `mapstructure:"connector_authentication"`
	Policies                       []string `mapstructure:"policies"`
	AwsSsmEnabled                  bool     `mapstructure:"aws_ssm_enabled"`
	AwsEC2ConnectEnabled           bool     `mapstructure:"aws_ec2_connect"`
	UpstreamUsername               string   `mapstructure:"upstream_username"`
	UpstreamPassword               string   `mapstructure:"upstream_password"`
	UpstreamIdentifyFile           string   `mapstructure:"upstream_identity_file"`
}

type EcsPlugin struct {
	Group                          string
	ConnectorAuthenticationEnabled bool     `mapstructure:"connector_authentication"`
	Policies                       []string `mapstructure:"policies"`
	TaskFilter                     []string `mapstructure:"aws_ecs_tasks"`
	ServiceFilter                  []string `mapstructure:"aws_ecs_services"`
	ContainerFilter                []string `mapstructure:"aws_ecs_containers"`
}

type K8Plugin struct {
	Group                          string
	Namespace                      string
	AllowedEmailAddresses          []string `mapstructure:"allowed_email_addresses"`
	AllowedEmailDomains            []string `mapstructure:"allowed_email_domains"`
	ConnectorAuthenticationEnabled bool     `mapstructure:"connector_authentication"`
	Policies                       []string `mapstructure:"policies"`
	UpstreamUsername               string   `mapstructure:"upstream_username"`
	UpstreamPassword               string   `mapstructure:"upstream_password"`
	UpstreamIdentifyFile           string   `mapstructure:"upstream_identity_file"`
}

type NetworkPlugin struct {
	Scan_interval                  int64                           `mapstructure:"scan_interval"`
	Group                          string                          `mapstructure:"group"`
	AllowedEmailAddresses          []string                        `mapstructure:"allowed_email_addresses"`
	AllowedEmailDomains            []string                        `mapstructure:"allowed_email_domains"`
	Networks                       map[string]NetworkPluginNetwork `mapstructure:"networks"`
	Policies                       []string                        `mapstructure:"policies"`
	ConnectorAuthenticationEnabled bool                            `mapstructure:"connector_authentication"`
}

type NetworkPluginNetwork struct {
	Interfaces []string `mapstructure:"interfaces"`
	Subnets    []string `mapstructure:"subnets"`
	Ports      []uint16 `mapstructure:"ports"`
}

type Connector struct {
	Name                string
	AwsRegion           string `mapstructure:"aws-region"`
	SSMAwsRegion        string `mapstructure:"ssm-aws-region"`
	AwsProfile          string `mapstructure:"aws-profile"`
	ProviderRegion      string `mapstructure:"provider-region"`
	ProviderEnv         string `mapstructure:"provider-enviroment"`
	ProviderEnvironment string `mapstructure:"provider-environment"`
	ProviderType        string `mapstructure:"provider-type"`
}

func (c *Connector) GetProviderEnvironment() string {
	if c.ProviderEnvironment != "" {
		return c.ProviderEnvironment
	}
	return c.ProviderEnv
}

type SocketParams []map[string]SocketConfig

type Config struct {
	Credentials   Credentials
	Sockets       SocketParams
	Connector     Connector
	AwsGroups     []ConnectorGroups `mapstructure:"aws_groups"`
	EcsPlugin     []EcsPlugin       `mapstructure:"aws_ecs_ssm"`
	DockerPlugin  []ConnectorGroups `mapstructure:"docker_plugin"`
	NetworkPlugin []NetworkPlugin   `mapstructure:"network_plugin"`
	K8Plugin      []K8Plugin        `mapstructure:"k8_plugin"`
}

func (c *Config) Validate() error {
	if c.Connector.Name == "" {
		return fmt.Errorf("connector.name is required")
	} else if validateName(c.Connector.Name) != nil {
		return ErrInvalidConnectorName
	}

	return nil
}

func validateName(name string) error {
	re := regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,198}[a-zA-Z0-9])?$`)
	if !re.Match([]byte(name)) {
		return errors.New("invalid name")
	}

	return nil
}

func NewConfig() *Config {
	return &Config{}
}

type ConfigParser struct {
}

func NewConfigParser() *ConfigParser {
	return &ConfigParser{}
}

func (c *ConfigParser) Parse(configPath string) (*Config, error) {
	viper.SetConfigType("yaml")
	viper.AddConfigPath(path.Dir(configPath))
	viper.SetConfigFile(configPath)

	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}

	var cfg Config

	err = viper.Unmarshal(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *ConfigParser) LoadSSMInConfig(ssmAPI ssmiface.SSMAPI, cfg *Config) error {
	for _, socketMap := range cfg.Sockets {
		for k, v := range socketMap {
			if strings.HasPrefix(v.DatabaseCredentials, "aws:ssm:") {
				jsonCreds := SetupSSMField(ssmAPI, v.DatabaseCredentials)

				var mapCreds map[string]interface{}
				err := json.Unmarshal([]byte(jsonCreds), &mapCreds)
				if err != nil {
					return err
				}
				v.UpstreamUser = fmt.Sprint(mapCreds["username"])
				v.UpstreamPassword = fmt.Sprint(mapCreds["password"])

				if value, ok := mapCreds["engine"]; ok {
					v.UpstreamType = fmt.Sprint(value)
				}

				if value, ok := mapCreds["type"]; ok {
					v.UpstreamType = fmt.Sprint(value)
				}

				v.Host = fmt.Sprint(mapCreds["host"])
			}

			if strings.HasPrefix(v.UpstreamPassword, "aws:ssm:") {
				v.UpstreamPassword = SetupSSMField(ssmAPI, v.UpstreamPassword)
			}

			if strings.HasPrefix(v.UpstreamType, "aws:ssm:") {
				v.UpstreamType = SetupSSMField(ssmAPI, v.UpstreamType)
			}

			if strings.HasPrefix(v.UpstreamUser, "aws:ssm:") {
				v.UpstreamUser = SetupSSMField(ssmAPI, v.UpstreamUser)
			}

			if strings.HasPrefix(v.Host, "aws:ssm:") {
				v.Host = SetupSSMField(ssmAPI, v.Host)
			}

			socketMap[k] = v
		}
	}

	if strings.HasPrefix(cfg.Credentials.Username, "aws:ssm:") {
		cfg.Credentials.Username = SetupSSMField(ssmAPI, cfg.Credentials.Username)
	}

	if strings.HasPrefix(cfg.Credentials.Token, "aws:ssm:") {
		cfg.Credentials.Token = SetupSSMField(ssmAPI, cfg.Credentials.Token)
	}

	if strings.HasPrefix(cfg.Credentials.Password, "aws:ssm:") {
		cfg.Credentials.Password = SetupSSMField(ssmAPI, cfg.Credentials.Password)
	}

	return nil
}

func FetchFromSSM(svc ssmiface.SSMAPI, param string) (*ssm.GetParameterOutput, error) {
	results, err := svc.GetParameter(&ssm.GetParameterInput{
		Name:           aws.String(param),
		WithDecryption: aws.Bool(true),
	})

	return results, err
}

func StartSSMSession(cfg *Config) (ssmiface.SSMAPI, error) {
	sess, err := session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
		Profile:           cfg.Connector.AwsProfile,
		Config: aws.Config{
			Region: &cfg.Connector.SSMAwsRegion,
		},
	})

	if err != nil {
		log.Printf("failed to create aws session: %v", err)
		return nil, err
	}

	return ssm.New(sess), nil
}

func SetupSSMField(svc ssmiface.SSMAPI, key string) string {
	spllitedKey := strings.Split(key, "aws:ssm:")
	output, err := FetchFromSSM(svc, spllitedKey[1])
	if err != nil {
		log.Printf("we couldn't fetch the key %v from ssm: %v\n", spllitedKey, err)
		return ""
	}

	if output != nil && output.Parameter != nil && output.Parameter.Value != nil {
		return *output.Parameter.Value
	}

	return ""
}
