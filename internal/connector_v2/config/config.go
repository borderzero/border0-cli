package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/borderzero/border0-cli/internal/util"
	"github.com/borderzero/border0-cli/lib/varsource"
	"gopkg.in/yaml.v3"
)

const (
	envNameConfigFile                 = "BORDER0_CONFIG_FILE"
	envNameToken                      = "BORDER0_TOKEN"
	envNameConnectorServer            = "BORDER0_CONNECTOR_SERVER"
	envNameConnectorInsecureTransport = "BORDER0_CONNECTOR_INSECURE_TRANSPORT"
	envNameTunnelServer               = "BORDER0_TUNNEL_SERVER"

	envNamePrefixBorder0Var = "BORDER0_VAR_"

	envNameAndValueDelimeter = "="

	defaultCredentialsFilePath = ".border0/config.yaml"

	defaultConnectorServer = "capi.border0.com:443"
	defaultTunnelServer    = "tunnel.border0.com"
)

// Configuration represents (static) connector configuration
type Configuration struct {
	Token                      string            `yaml:"token"`
	ConnectorId                string            `yaml:"connector_id,omitempty"`
	ConnectorServer            string            `yaml:"connector_server,omitempty"`
	ConnectorInsecureTransport bool              `yaml:"connector_insecure,omitempty"`
	TunnelServer               string            `yaml:"tunnel_server,omitempty"`
	Variables                  map[string]string `yaml:"variables,omitempty"`
}

// GetConfiguration looks for credentials and variables in the standard variable chain.
// i.e. environment variabels take priority and override any values in config files.
func GetConfiguration(ctx context.Context, configFilePath string) (*Configuration, error) {
	vs := varsource.NewDefaultVariableSource()
	config := &Configuration{
		ConnectorServer:            defaultConnectorServer,
		ConnectorInsecureTransport: false,
		TunnelServer:               defaultTunnelServer,
	}

	path := ""
	if configFilePath != "" {
		path = configFilePath
	}
	if os.Getenv(envNameConfigFile) != "" {
		path = os.Getenv(envNameConfigFile)
	}

	if path != "" {
		_, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to find configuration file specified via environment variable %s (%s): %v",
				envNameConfigFile,
				path,
				err,
			)
		}
		if err = unmarshalConfiguration(path, config); err != nil {
			return nil, fmt.Errorf(
				"failed to read configuration file specified via environment variable %s (%s): %v",
				envNameConfigFile,
				path,
				err,
			)
		}
	} else {
		// otherwise try the default configuration file path for config
		if hd, err := util.GetUserHomeDir(); err == nil {
			path = fmt.Sprintf("%s/%s", hd, defaultCredentialsFilePath)
			if _, err = os.Stat(path); err != nil {
				// when the default config file path is used, we only error out when
				// the failure is not due to a missing config file. This is because
				// the token and config might still be present in the environment.
				if !errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf(
						"failed to read default configuration file (%s): %v",
						defaultCredentialsFilePath,
						err,
					)
				}
				// pass
			} else {
				if err = unmarshalConfiguration(path, config); err != nil {
					return nil, fmt.Errorf(
						"failed to read the default configuration file (%s): %v",
						path,
						err,
					)
				}
			}
		}
	}

	// if there is a token in the environment, overwrite that read from config file
	if os.Getenv(envNameToken) != "" {
		config.Token = os.Getenv(envNameToken)
	}

	// if there was no token in env nor in config file, error out
	if config.Token == "" {
		return nil, errors.New("no Border0 token in credentials chain")
	}

	// if the token is a pointer to some upstream source, fetch it
	token, err := vs.GetVariable(ctx, config.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to process token value: %v", err)
	}
	config.Token = token

	// if there is a connector server in the environment, overwrite that read from config file
	if os.Getenv(envNameConnectorServer) != "" {
		config.ConnectorServer = os.Getenv(envNameConnectorServer)
	}

	// if the connector server is a pointer to some upstream source, fetch it
	connectorServer, err := vs.GetVariable(ctx, config.ConnectorServer)
	if err != nil {
		return nil, fmt.Errorf("failed to process connectorServer value: %v", err)
	}
	config.ConnectorServer = connectorServer

	// if there is a connector insecure in the environment, overwrite that read from config file
	if os.Getenv(envNameConnectorInsecureTransport) != "" {
		connectorInsecureTransport, err := strconv.ParseBool(os.Getenv(envNameConnectorInsecureTransport))
		if err != nil {
			return nil, fmt.Errorf("failed to parse connectorInsecureTransport value: %v", err)
		}

		config.ConnectorInsecureTransport = connectorInsecureTransport
	}

	// if there is a tunnel server in the environment, overwrite that read from config file
	if os.Getenv(envNameTunnelServer) != "" {
		config.TunnelServer = os.Getenv(envNameTunnelServer)
	}

	// if the connector server is a pointer to some upstream source, fetch it
	tunnelServer, err := vs.GetVariable(ctx, config.TunnelServer)
	if err != nil {
		return nil, fmt.Errorf("failed to process tunnelServer value: %v", err)
	}
	config.TunnelServer = tunnelServer

	// if there were no variables defined in a config file, init empty map
	if config.Variables == nil {
		config.Variables = make(map[string]string)
	}

	// for all environment variables, if any start with the border0 variable prefix
	// use them to populate the variables map (and overwrite any which are present in the
	// environment that were present in the config file)
	for _, kv := range os.Environ() {
		// split env between key and value... this will *always* have 2 parts
		kvParts := strings.SplitN(kv, envNameAndValueDelimeter, 2)
		name, value := kvParts[0], kvParts[1]

		// skip all env vars that do not have the border0 variable prefix
		if !strings.HasPrefix(name, envNamePrefixBorder0Var) {
			continue
		}

		// add variable to configuration variables map
		config.Variables[strings.TrimPrefix(name, envNamePrefixBorder0Var)] = value
	}

	// fetch all variables which are pointers to other variables in upstream servies
	vars, err := vs.GetVariables(ctx, config.Variables)
	if err != nil {
		return nil, fmt.Errorf("failed to get upstream variables: %v", err)
	}
	config.Variables = vars

	return config, nil
}

func unmarshalConfiguration(path string, config *Configuration) error {
	// read config file
	configFileBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read configuration file at %s: %v", path, err)
	}
	// decode config yaml
	if err = yaml.Unmarshal(configFileBytes, config); err != nil {
		return fmt.Errorf("failed to decode config file data as yaml: %v", err)
	}
	// success!
	return nil
}

// SetBorder0Token sets the connector token in the environment variable BORDER0_TOKEN
func SetBorder0Token(token string) error {
	return os.Setenv(envNameToken, token)
}
