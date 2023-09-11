package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/kardianos/service"
)

const (
	defaultServiceName        = "border0"
	defaultServiceDisplayName = "Border0 Connector"
	defaultServiceDescription = "Border0 Connector Service"
	defaultBinaryName         = "border0"
)

// Option represents a configuration option for the Border0 Connector service.
type Option func(*configuration)

// WithServiceName overrides the default service name.
func WithServiceName(serviceName string) Option {
	return func(c *configuration) { c.serviceName = serviceName }
}

// WithServiceDisplayName overrides the default service display name.
func WithServiceDisplayName(serviceDisplayName string) Option {
	return func(c *configuration) { c.serviceDisplayName = serviceDisplayName }
}

// WithServiceDescription overrides the default service description.
func WithServiceDescription(serviceDescription string) Option {
	return func(c *configuration) { c.serviceDescription = serviceDescription }
}

// WithBinaryName overrides the default binary name.
func WithBinaryName(binary string) Option {
	return func(c *configuration) { c.binaryName = binary }
}

// WithConfigurationFilePath sets the configuration file path.
func WithConfigurationFilePath(path string) Option {
	return func(c *configuration) { c.configFilePath = path }
}

type configuration struct {
	serviceName        string
	serviceDisplayName string
	serviceDescription string
	binaryName         string
	configFilePath     string
}

// GetConnectorService returns the service.Service
// that wraps the Border0 Connector daemon service.
func GetConnectorService(opts ...Option) (service.Service, error) {
	setUpLogging()

	// initialize config and apply options
	config := &configuration{
		serviceName:        defaultServiceName,
		serviceDisplayName: defaultServiceDisplayName,
		serviceDescription: defaultServiceDescription,
		binaryName:         defaultBinaryName,
	}
	for _, opt := range opts {
		opt(config)
	}
	// define internal service config
	internalService := &connectorService{}
	// look for binary in binaries PATH(s)
	executablePath, err := exec.LookPath(config.binaryName)
	if err != nil {
		executablePath = config.binaryName
		// return nil, fmt.Errorf("Failed to find executable %s: %v", config.binaryName, err)
	}
	// define arguments
	args := []string{"connector", "start"}
	if config.configFilePath != "" {
		args = append(args, "--config", config.configFilePath)
	}
	// define system service config (abstracts away OS specifics)
	systemService := &service.Config{
		Name:        config.serviceName,
		DisplayName: config.serviceDisplayName,
		Description: config.serviceDescription,
		Executable:  executablePath,
		Arguments:   args,
	}
	// initialize new service object
	svc, err := service.New(internalService, systemService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize border0 connector service object: %v", err)
	}
	return svc, nil
}

// IsInstalled returns true if the Border0 Connector service is already installed.
func IsInstalled() (bool, error) {
	connectorSvc, err := GetConnectorService()
	if err != nil {
		return false, fmt.Errorf("failed to initialize new connector service object: %v", err)
	}
	if _, err = connectorSvc.Status(); err != nil {
		if strings.Contains(err.Error(), "the service is not installed") {
			return false, nil
		}
		return false, fmt.Errorf("failed to get connector service status: %v", err)
	}
	return true, nil
}

func setUpLogging() {
	logFile, err := os.OpenFile("C:/Users/mysocket/border0/my_log.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}
	log.SetOutput(logFile)
	log.Println("Logger initialized.")
}

type connectorService struct {
	stopCh chan struct{}
}

func (cs *connectorService) Start(svc service.Service) error {
	log.Println("Service start triggered.")
	cs.stopCh = make(chan struct{})
	go cs.run()
	return nil
}

func (cs *connectorService) run() {
	log.Println("Service started.")
	<-cs.stopCh
	return
}

func (cs *connectorService) Stop(svc service.Service) error {
	log.Println("Service being stopped.")
	close(cs.stopCh) // send the signal to stop
	return nil
}
