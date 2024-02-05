//go:build windows
// +build windows

package wintundll

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Ensure ensures the presense of the wintun dll.
func Ensure(opts ...EnsureOption) error {
	config := &configuration{
		downloadURL:     defaultDownloadURL,
		downloadTimeout: defaultDownloadTimeout,
	}
	for _, opt := range opts {
		opt(config)
	}

	admin, err := isRunningAsAdministator()
	if err != nil {
		fmt.Errorf("failed to determine if executable is running as administrator: %v", err)
	}
	if !admin {
		return errors.New("executable is not running as administrator")
	}

	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %v", err)
	}
	systemTempDir := os.TempDir()

	// If the executable is in a temporary directory
	// then the program is likely being ran with "go run".
	// In that case, we use the current working directory instead.
	//
	// This will unfortunately fail for instances where the program
	// is being ran in a temporary path from outside of that directory.
	//
	// FIXME: improve this
	if strings.HasPrefix(executablePath, systemTempDir) {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %v", err)
		}
		executablePath = filepath.Join(wd, "main.go")
	}

	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86" // wintun bundles 386 as x86
	}

	_, err = syscall.LoadDLL(filepath.Join(filepath.Dir(executablePath), "wintun.dll"))
	if err == nil {
		return nil
	}

	err = downloadAndMoveFromZip(
		http.Client{Timeout: config.downloadTimeout},
		config.downloadURL,
		fmt.Sprintf("wintun/bin/%s/wintun.dll", arch),
		filepath.Join(filepath.Dir(executablePath), "wintun.dll"),
	)
	if err != nil {
		return fmt.Errorf("failed to get wintun.dll from remote: %v", err)
	}

	_, err = syscall.LoadDLL(filepath.Join(filepath.Dir(executablePath), "wintun.dll"))
	if err != nil {
		return fmt.Errorf("still failed to load wintun.dll after fresh download: %v", err)
	}

	return nil
}

// isRunningAsAdministator returns true if the executable is running
// as a system administrator. This function is copied directly from
// https://github.com/golang/go/issues/28804 with additional comments.
func isRunningAsAdministator() (bool, error) {
	var sid *windows.SID

	// Although this looks scary, it is directly copied from the
	// official windows documentation. The Go API for this is a
	// direct wrap around the official C++ API.
	// See https://docs.microsoft.com/en-us/windows/desktop/api/securitybaseapi/nf-securitybaseapi-checktokenmembership
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,      // identAuth *windows.SidIdentifierAuthority
		2,                                   // subAuth byte
		windows.SECURITY_BUILTIN_DOMAIN_RID, // subAuth0 uint32
		windows.DOMAIN_ALIAS_RID_ADMINS,     // subAuth1 uint32
		0,                                   // subAuth2 uint32
		0,                                   // subAuth3 uint32
		0,                                   // subAuth4 uint32
		0,                                   // subAuth5 uint32
		0,                                   // subAuth6 uint32
		0,                                   // subAuth7 uint32
		&sid,                                // sid **windows.SID
	)
	if err != nil {
		return false, fmt.Errorf("failed to initialize admin SID: %v", err)
	}
	defer windows.FreeSid(sid)

	// This appears to cast a null pointer so I'm not sure why this
	// works, but this guy says it does and it Works for Me™:
	// https://github.com/golang/go/issues/28804#issuecomment-438838144
	token := windows.Token(0)
	defer token.Close()

	// check if the token is a member of the admin SID
	admin, err := token.IsMember(sid)
	if err != nil {
		return false, fmt.Errorf("token membership error: %v", err)
	}
	return admin, nil
}
