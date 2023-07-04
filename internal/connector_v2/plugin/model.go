package plugin

import (
	"context"

	"github.com/borderzero/discovery"
	"github.com/borderzero/discovery/engines"
	"go.uber.org/zap"
)

type Plugin interface {
	Start(ctx context.Context, resultChan chan *PluginDiscoveryResults) error
	Stop() error
}

type pluginImpl struct {
	logger *zap.Logger
	ID     string
	Name   string
	engine *engines.ContinuousEngine
	cancel context.CancelFunc
}

type PluginDiscoveryResults struct {
	PluginID string
	Result   *discovery.Result
}
