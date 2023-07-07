package plugin

import (
	"context"

	"github.com/borderzero/discovery"
)

// Plugin represents actions required of a plugin implementation.
type Plugin interface {
	Start(ctx context.Context, results chan *PluginDiscoveryResults) error
	Stop() error
}

// PluginDiscoveryResults represents results of a single plugin
type PluginDiscoveryResults struct {
	PluginID string
	Result   *discovery.Result
}
