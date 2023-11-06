package sqlauthproxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	uploadBufferThreshold = 1024 * 1024
	uploadInterval        = 30 * time.Second
)

type recording struct {
	logger      *zap.Logger
	api         border0.Border0API
	sessionKey  string
	socketID    string
	recordingID uuid.UUID
	zipWriter   *gzip.Writer
	buf         bytes.Buffer
	uploadLock  sync.Mutex
}

type message struct {
	Time         int64   `json:"time"`
	Database     string  `json:"database"`
	Command      string  `json:"command"`
	Status       *uint16 `json:"status"`
	Duration     int64   `json:"duration"`
	Rows         *int64  `json:"rows"`
	AffectedRows *uint64 `json:"affected_rows"`
	Result       *string `json:"result"`
}

func newRecording(logger *zap.Logger, socketID, sessionKey string, api border0.Border0API) (*recording, error) {
	return &recording{
		logger:      logger,
		sessionKey:  sessionKey,
		socketID:    socketID,
		api:         api,
		recordingID: uuid.New(),
	}, nil
}

func (r *recording) Record(messageChan chan message) error {
	r.zipWriter = gzip.NewWriter(&r.buf)

	go func() {
		shouldUpload := true
		dateWritten := false

		defer func() {
			if shouldUpload && dateWritten {
				if err := r.upload(); err != nil {
					r.logger.Error("failed to upload recording", zap.Error(err))
					return
				}
			}
		}()

		timer := time.NewTimer(uploadInterval)

		for {
			select {
			case message, open := <-messageChan:
				if !open {
					return
				}

				logJson, err := json.Marshal(message)
				if err != nil {
					r.logger.Error("failed to marshal message", zap.Error(err))
					shouldUpload = false
					return
				}

				logJson = append([]byte(logJson), "\n"...)

				if _, err := r.zipWriter.Write([]byte(logJson)); err != nil {
					r.logger.Error("failed to write to recording", zap.Error(err))
					shouldUpload = false
					return
				}

				dateWritten = true

				if r.buf.Len() > uploadBufferThreshold {
					if err := r.upload(); err != nil {
						r.logger.Error("failed to upload recording", zap.Error(err))
						shouldUpload = false
						return
					}

					timer.Reset(uploadInterval)
					dateWritten = false
				}
			case <-timer.C:
				if dateWritten {
					if err := r.upload(); err != nil {
						r.logger.Error("failed to upload recording", zap.Error(err))
						shouldUpload = false
						return
					}

					dateWritten = false
				}

				timer.Reset(uploadInterval)
			}
		}
	}()

	return nil
}

func (r *recording) upload() error {
	r.uploadLock.Lock()
	defer r.uploadLock.Unlock()

	if err := r.zipWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush session log file: %s", err)
	}

	if err := r.zipWriter.Close(); err != nil {
		return fmt.Errorf("failed to close session log file: %s", err)
	}

	uploadBuffer := make([]byte, r.buf.Len())
	copy(uploadBuffer, r.buf.Bytes())
	r.buf.Reset()
	r.zipWriter = gzip.NewWriter(&r.buf)

	go func(uploadBuffer []byte) {
		r.uploadLock.Lock()
		defer r.uploadLock.Unlock()

		if err := r.api.UploadRecording(uploadBuffer, r.socketID, r.sessionKey, r.recordingID.String()); err != nil {
			r.logger.Error("failed to upload recording", zap.Error(err))
			return
		}
	}(uploadBuffer)

	return nil
}
