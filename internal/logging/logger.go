package logging

import (
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func BuildProduction() (*zap.Logger, error) {
	// fetch log level by env
	logLevel := zapcore.Level(fetchLogLevelByEnv())
	c := zap.NewProductionConfig()
	c.Level = zap.NewAtomicLevelAt(logLevel)
	c.EncoderConfig.StacktraceKey = ""
	c.EncoderConfig.CallerKey = ""
	c.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	log, err := c.Build()
	if err != nil {
		return nil, err
	}
	return log, nil
}

func fetchLogLevelByEnv() zapcore.Level {
	loglevel := os.Getenv("BORDER0_LOG_LEVEL")

	switch strings.ToLower(loglevel) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
