package wintundll

import (
	"time"
)

const (
	defaultDownloadURL     = "https://www.wintun.net/builds/wintun-0.14.1.zip"
	defaultDownloadTimeout = time.Second * 5
)

type configuration struct {
	downloadURL     string
	downloadTimeout time.Duration
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
