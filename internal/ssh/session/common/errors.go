package common

import "strings"

// IsBinaryNotFound returns true if a string corresponds
// to an error for a binary not being found in a system.
func IsBinaryNotFound(errStr string) bool {
	for _, canary := range []string{
		"no such file or directory",
		"executable file not found",
		"command terminated with exit code 127",
	} {
		if strings.Contains(errStr, canary) {
			return true
		}
	}

	return false
}
