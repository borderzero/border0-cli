//go:build windows
// +build windows

package vpnlib

import (
	"fmt"
	"time"

	"github.com/borderzero/water"
	"github.com/borderzero/wintundll-downloader-go/wintundll"
)

// CreateTun creates a TUN interface for Windows
// we're using the the winTun driver, which is a TUN driver for Windows
// https://www.wintun.net/
// we'll use the wintundll-downloader-go library to ensure the driver is installed
func CreateTun() (ifce *water.Interface, err error) {

	err = wintundll.Ensure(
		wintundll.WithDownloadURL("https://www.wintun.net/builds/wintun-0.14.1.zip"),
		wintundll.WithDownloadTimeout(time.Second*10),
		wintundll.WithDllPathToEnsure(`C:\Windows\System32\wintun.dll`),
	)
	if err != nil {
		return nil, fmt.Errorf("error ensuring Wintun driver is installed: %w", err)
	}

	waterConfig := water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name: "border0VPN",
		},
	}
	// Create a TUN interface with the given configuration.
	iface, err := water.New(waterConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating TUN interface: %v", err)
	}

	return iface, nil
}
