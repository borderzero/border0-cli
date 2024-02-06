//go:build !linux && !windows
// +build !linux,!windows

package vpnlib

import (
	"fmt"

	"github.com/borderzero/water"
)

func CreateTun() (ifce *water.Interface, err error) {
	waterConfig := water.Config{DeviceType: water.TUN}
	iface, err := water.New(waterConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating TUN interface: %v", err)
	}
	return iface, nil
}
