//go:build !windows
// +build !windows

package wintundll

import (
	"fmt"
	"runtime"
)

// Ensure returns an incompatibility error (on non-windows).
func Ensure(opts ...EnsureOption) error {
	return fmt.Errorf("This software is only compatible with GOOS windows (not %s)", runtime.GOOS)
}
