package plugin

import (
	pb "github.com/borderzero/border0-proto/connector"
)

type Plugin interface {
	Update(config *pb.PluginConfig) error
	Delete() error
}
