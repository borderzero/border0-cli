//go:build openbsd
// +build openbsd

package service

import "fmt"

type service interface {
	Install(args ...string) (string, error)
	Remove() (string, error)
	Start() (string, error)
	Stop() (string, error)
	Status() (string, error)
}

func New(name, description string) (service, error) {
	return nil, fmt.Errorf("service not supported on openbsd")
}
