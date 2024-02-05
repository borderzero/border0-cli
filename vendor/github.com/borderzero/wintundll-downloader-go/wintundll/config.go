package wintundll

import (
	"fmt"
	"runtime"
	"time"
)

type configuration struct {
	downloadURL          string
	downloadTimeout      time.Duration
	dllPathInUnzippedDir string
	dllPathInToEnsure    string
}

// EnsureOption represents a configuration option
// for the Ensure function in this package.
type EnsureOption func(*configuration)

// WithDownloadURL returns an EnsureOption to set
// a non-default value for the download url.
func WithDownloadURL(url string) EnsureOption {
	return func(c *configuration) { c.downloadURL = url }
}

// WithDownloadTimeout returns an EnsureOption to set
// a non-default value for the download tiemout.
func WithDownloadTimeout(timeout time.Duration) EnsureOption {
	return func(c *configuration) { c.downloadTimeout = timeout }
}

// WithDllPathInUnzippedDir returns an EnsureOption to set
// a non-default value for the location of the dll file in the
// unzipped downloaded folder.
func WithDllPathInUnzippedDir(path string) EnsureOption {
	return func(c *configuration) { c.dllPathInUnzippedDir = path }
}

// WithDllPathToEnsure returns an EnsureOption to set a non-default
// value for the location where to ensure the dll file.
func WithDllPathToEnsure(path string) EnsureOption {
	return func(c *configuration) { c.dllPathInToEnsure = path }
}

func getConfiguration(opts ...EnsureOption) *configuration {
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86" // wintun bundles 386 as x86
	}
	config := &configuration{
		downloadURL:          "https://www.wintun.net/builds/wintun-0.14.1.zip",
		downloadTimeout:      time.Second * 5,
		dllPathInUnzippedDir: fmt.Sprintf("wintun/bin/%s/wintun.dll", arch),
		dllPathInToEnsure:    `C:\Windows\System32\wintun.dll`,
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}
