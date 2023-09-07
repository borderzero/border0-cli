package ssh

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/aws/session-manager-plugin/src/config"
	"github.com/aws/session-manager-plugin/src/log"
	"github.com/aws/session-manager-plugin/src/message"
	"github.com/aws/session-manager-plugin/src/retry"
	awsSession "github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session"
	"golang.org/x/crypto/ssh"
)

const (
	StdinBufferLimit = 1024
)

type ShellSession struct {
	awsSession.Session
	retryParams     retry.RepeatableExponentialRetryer
	sshChannel      ssh.Channel
	sshWindowHeight int
	sshWindowWidth  int
	ssmWindowHeight int
	ssmWindowWidth  int
}

func (s *ShellSession) Initialize(log log.T, sessionVar *ShellSession) {
	s.DataChannel.RegisterOutputStreamHandler(s.ProcessStreamMessagePayload, true)
	s.DataChannel.GetWsChannel().SetOnMessage(
		func(input []byte) {
			s.DataChannel.OutputMessageHandler(log, s.Stop, s.SessionId, input)
		})

	go s.handleTerminalResize()
}

func (s *ShellSession) Stop() {
	s.sshChannel.Close()
}

// StartSession takes input and write it to data channel
func (s *ShellSession) SetSessionHandlers(log log.T) (err error) {
	return s.handleKeyboardInput(log)
}

// ProcessStreamMessagePayload prints payload received on datachannel to console
func (s ShellSession) ProcessStreamMessagePayload(log log.T, outputMessage message.ClientMessage) (isHandlerReady bool, err error) {
	fmt.Fprint(s.sshChannel, string(outputMessage.Payload))
	return true, nil
}

// handleKeyboardInput handles input entered by customer on terminal
func (s *ShellSession) handleKeyboardInput(log log.T) (err error) {
	var (
		stdinBytesLen int
	)

	stdinBytes := make([]byte, StdinBufferLimit)
	for {
		if stdinBytesLen, err = s.sshChannel.Read(stdinBytes); err != nil {
			log.Errorf("Unable read from Stdin: %v", err)
			break
		}

		if err = s.Session.DataChannel.SendInputDataMessage(log, message.Output, stdinBytes[:stdinBytesLen]); err != nil {
			log.Errorf("Failed to send UTF8 char: %v", err)
			break
		}
		// sleep to limit the rate of data transfer
		time.Sleep(time.Millisecond)
	}
	return
}

func (s *ShellSession) ResumeSessionHandler(log log.T) (err error) {
	s.TokenValue, err = s.GetResumeSessionParams(log)
	if err != nil {
		log.Errorf("Failed to get token: %v", err)
		return
	} else if s.TokenValue == "" {
		return
	}
	s.DataChannel.GetWsChannel().SetChannelToken(s.TokenValue)
	err = s.DataChannel.Reconnect(log)
	return
}

func (s *ShellSession) OpenDataChannel(log log.T) (err error) {
	s.retryParams = retry.RepeatableExponentialRetryer{
		GeometricRatio:      config.RetryBase,
		InitialDelayInMilli: rand.Intn(config.DataChannelRetryInitialDelayMillis) + config.DataChannelRetryInitialDelayMillis,
		MaxDelayInMilli:     config.DataChannelRetryMaxIntervalMillis,
		MaxAttempts:         config.DataChannelNumMaxRetries,
	}

	s.DataChannel.Initialize(log, s.ClientId, s.SessionId, s.TargetId, s.IsAwsCliUpgradeNeeded)
	s.DataChannel.SetWebsocket(log, s.StreamUrl, s.TokenValue)
	s.DataChannel.GetWsChannel().SetOnMessage(
		func(input []byte) {
			s.DataChannel.OutputMessageHandler(log, s.Stop, s.SessionId, input)
		})
	s.DataChannel.RegisterOutputStreamHandler(s.ProcessFirstMessage, false)

	if err = s.DataChannel.Open(log); err != nil {
		log.Errorf("Retrying connection for data channel id: %s failed with error: %s", s.SessionId, err)
		s.retryParams.CallableFunc = func() (err error) { return s.DataChannel.Reconnect(log) }
		if err = s.retryParams.Call(); err != nil {
			log.Error(err)
		}
	}

	s.DataChannel.GetWsChannel().SetOnError(
		func(err error) {
			log.Errorf("Trying to reconnect the session: %v with seq num: %d", s.StreamUrl, s.DataChannel.GetStreamDataSequenceNumber())
			s.retryParams.CallableFunc = func() (err error) { return s.ResumeSessionHandler(log) }
			if err = s.retryParams.Call(); err != nil {
				log.Error(err)
			}
		})

	// Scheduler for resending of data
	s.DataChannel.ResendStreamDataMessageScheduler(log)

	return nil
}

func (s *ShellSession) handleWindowChange(width, height int) {
	s.sshWindowWidth = width
	s.sshWindowHeight = height
}

func (s *ShellSession) handleTerminalResize() {
	ticker := time.NewTicker(ResizeSleepInterval)
	for ; true; <-ticker.C {
		if s.ssmWindowHeight != s.sshWindowHeight || s.ssmWindowWidth != s.sshWindowWidth {
			sizeData := message.SizeData{
				Cols: uint32(s.sshWindowWidth),
				Rows: uint32(s.sshWindowHeight),
			}

			var inputSizeData []byte
			var err error

			if inputSizeData, err = json.Marshal(sizeData); err != nil {
				fmt.Printf("cannot marshall size data: %v", err)
				continue
			}

			if err := s.DataChannel.SendInputDataMessage(log.Logger(false, "border0"), message.Size, inputSizeData); err != nil {
				fmt.Printf("failed to Send size data: %v", err)
				continue
			}

			s.ssmWindowWidth = s.sshWindowWidth
			s.ssmWindowHeight = s.sshWindowHeight
		}
	}
}
