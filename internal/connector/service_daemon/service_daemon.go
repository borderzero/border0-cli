//go:build !openbsd
// +build !openbsd

package service_daemon

import (
	"runtime"

	"github.com/takama/daemon"
)

func New(name, description string) (daemon.Daemon, error) {
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
