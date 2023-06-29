package plugin

import (
	"context"
	"fmt"
	"strings"

	pb "github.com/borderzero/border0-proto/connector"
	"go.uber.org/zap"
)

func NewPlugin(ctx context.Context, l *zap.Logger, config *pb.PluginConfig) (Plugin, error) {
	switch strings.ToLower(config.Type) {
	case "aws ecs":
		plugin, err := NewAwsEcsPlugin(ctx, l, config)
		return plugin, err
	// case "aws ec2":
	// plugin, err := NewAwsEc2Plugin(ctx, l, config)
	// return plugin, err
	// case "aws rds":
	// plugin, err := NewAwsRdsPlugin(ctx, l, config)
	// return plugin, err
	default:
		return nil, fmt.Errorf("unknown plugin type: %s", config.Type)
	}
}
