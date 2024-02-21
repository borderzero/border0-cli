//go:build linux
// +build linux

package vpnlib

import (
	"fmt"

	"github.com/borderzero/water"
)

// CreateTun creates a TUN interface for Linux
func CreateTun() (ifce *water.Interface, err error) {

	// TUN interface configuration
	tunConfig := water.Config{
		DeviceType: water.TUN,
	}
	// tunConfig.Name = "tun0"

	tunIfce, err := water.New(tunConfig)

	if err != nil {
		return nil, fmt.Errorf("error creating TUN interface: %v", err)
	}
	return tunIfce, nil
}
