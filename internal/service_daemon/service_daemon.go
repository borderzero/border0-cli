//go:build !openbsd
// +build !openbsd

package service_daemon

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/takama/daemon"
)

// New initializes a new service daemon object with a given name and description.
func New(name, description string) (Service, error) {
	deamonType := daemon.SystemDaemon
	if runtime.GOOS == "darwin" {
		deamonType = daemon.GlobalDaemon
	}
	daemon, err := daemon.New(name, description, deamonType)
	if err != nil {
		return nil, err
	}
	// the default template for linux has a legacy path...
	if runtime.GOOS == "linux" {
		if err = daemon.SetTemplate(
			strings.ReplaceAll(
				daemon.GetTemplate(),
				"/var/run/",
				"/run/",
			),
		); err != nil {
			return nil, fmt.Errorf("failed to set service template: %v", err)
		}
	}
	return daemon, err
}
