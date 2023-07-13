package logger

import (
	"fmt"

	pb "github.com/borderzero/border0-proto/connector"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	debugLevel = "debug"
	infoLevel  = "info"
	warnLevel  = "warn"
	errorLevel = "error"
)

type Logger interface {
	LocalDebug(format string, args ...interface{})
	LocalInfo(format string, args ...interface{})
	LocalWarn(format string, args ...interface{})
	LocalError(format string, args ...interface{})

	Debug(format string, args ...interface{})
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})

	NewConnectorPluginLogger(pluginID string) *ConnectorLogger
}

type ConnectorLogger struct {
	logger   *zap.Logger
	sendFunc func(*pb.ControlStreamRequest) error
	socketID string
	pluginID string
}

func NewConnectorLogger(logger *zap.Logger, sendFunc func(*pb.ControlStreamRequest) error, connectorID string) *ConnectorLogger {
	return &ConnectorLogger{
		logger:   logger,
		sendFunc: sendFunc,
	}
}

func (l *ConnectorLogger) NewConnectorPluginLogger(pluginID string) *ConnectorLogger {
	newLogger := *l
	newLogger.pluginID = pluginID
	return &newLogger
}

func (l *ConnectorLogger) LocalDebug(format string, args ...interface{}) {
	l.logger.Debug(fmt.Sprintf(format, args...))
}

func (l *ConnectorLogger) LocalInfo(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l *ConnectorLogger) LocalWarn(format string, args ...interface{}) {
	l.logger.Warn(fmt.Sprintf(format, args...))
}

func (l *ConnectorLogger) LocalError(format string, args ...interface{}) {
	l.logger.Error(fmt.Sprintf(format, args...))
}

func (l *ConnectorLogger) Debug(format string, args ...interface{}) {
	l.LocalDebug(format, args...)

	if err := l.sendLog(debugLevel, fmt.Sprintf(format, args...)); err != nil {
		l.logger.Error("Failed to send log", zap.Error(err))
	}
}

func (l *ConnectorLogger) Info(format string, args ...interface{}) {
	l.LocalInfo(fmt.Sprintf(format, args...))

	if err := l.sendLog(infoLevel, fmt.Sprintf(format, args...)); err != nil {
		l.logger.Error("Failed to send log", zap.Error(err))
	}
}

func (l *ConnectorLogger) Warn(format string, args ...interface{}) {
	l.LocalWarn(fmt.Sprintf(format, args...))

	if err := l.sendLog(warnLevel, fmt.Sprintf(format, args...)); err != nil {
		l.logger.Error("Failed to send log", zap.Error(err))
	}
}

func (l *ConnectorLogger) Error(format string, args ...interface{}) {
	l.LocalError(fmt.Sprintf(format, args...))

	if err := l.sendLog(errorLevel, fmt.Sprintf(format, args...)); err != nil {
		l.logger.Error("Failed to send log", zap.Error(err))
	}
}

func (l *ConnectorLogger) sendLog(level, message string) error {
	return l.sendFunc(&pb.ControlStreamRequest{
		RequestType: &pb.ControlStreamRequest_Log{
			Log: &pb.Log{
				SocketId:  l.socketID,
				PluginId:  l.pluginID,
				Timestamp: timestamppb.Now(),
				Severity:  level,
				Message:   message,
			},
		},
	})
}
