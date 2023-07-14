package logger

import (
	"log"

	"github.com/borderzero/border0-cli/internal/logging"
	"go.uber.org/zap"
)

var Logger *zap.Logger

func init() {
	var err error
	Logger, err = logging.BuildProduction()
	if err != nil {
		log.Fatalf("can't initialize logger: %v", err)
	}
}
