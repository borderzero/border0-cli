//go:build !windows
// +build !windows

package wintundll

import (
	"fmt"
	"runtime"
)

// Ensure ensures the presence of the wintun dll in the system.
// It returns an incompatibility error on non-windows.
func Ensure(opts ...EnsureOption) error {
	return fmt.Errorf("This software is only compatible with GOOS windows (not %s)", runtime.GOOS)
}
