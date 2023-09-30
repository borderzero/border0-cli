//go:build windows
// +build windows

package process

// SetupUserAndGroups on Windows is a NO-OP because Windows doesn't support
// changing uid, gid, or groups in the same way as the other platforms.
func SetupUserAndGroups(params *Parameters) error {
	return nil
}
