//go:build !windows
// +build !windows

package process

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// SetupUserAndGroups changes the uid, gid, and groups of the current process.
func SetupUserAndGroups(params *Parameters) error {
	euid := os.Geteuid()
	egid := os.Getegid()
	if euid != 0 && params.UID != euid {
		return fmt.Errorf("command must be run as root or with sudo")
	}
	// darwin allows a maximum of 16 groups
	if runtime.GOOS == "darwin" && len(params.Groups) > 16 {
		params.Groups = params.Groups[:16]
	}

	if egid != params.GID || euid != params.UID {
		if err := syscall.Setgroups(params.Groups); err != nil {
			return fmt.Errorf("failed to set groups: %w", err)
		}
		if egid != params.GID {
			if err := syscall.Setgid(params.GID); err != nil {
				return fmt.Errorf("failed to set gid: %w", err)
			}
		}
		if euid != params.UID {
			if err := syscall.Setuid(params.UID); err != nil {
				return fmt.Errorf("failed to set uid: %w", err)
			}
		}
	}

	return nil
}
