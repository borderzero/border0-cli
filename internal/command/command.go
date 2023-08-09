package command

import "os/exec"

func Exists(name string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	return true
}
