//go:build !openbsd
// +build !openbsd

package service_daemon

import (
	"runtime"

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
	return daemon, err
}