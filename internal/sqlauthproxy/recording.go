package sqlauthproxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type recording struct {
	logger      *zap.Logger
	api         border0.Border0API
	sessionKey  string
	recordingID uuid.UUID
	zipWriter   *gzip.Writer
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

func newRecording(logger *zap.Logger, sessionKey string, api border0.Border0API) (*recording, error) {
	return &recording{
		logger:      logger,
		sessionKey:  sessionKey,
		api:         api,
		recordingID: uuid.New(),
	}, nil
}

func (r *recording) Record(messageChan chan message) error {
	var buf bytes.Buffer
	r.zipWriter = gzip.NewWriter(&buf)

	go func() {
		for {
			message, open := <-messageChan
			if !open {
				break
			}

			logJson, _ := json.Marshal(message)
			logJson = append([]byte(logJson), "\n"...)

			if _, err := r.zipWriter.Write([]byte(logJson)); err != nil {
				r.logger.Error("failed to write to recording", zap.Error(err))
				return
			}

			if buf.Len() > 1024*1024 { // 1MB
				if err := r.zipWriter.Flush(); err != nil {
					r.logger.Error("failed to flush session log file", zap.Error(err))
					return
				}

				if err := r.zipWriter.Close(); err != nil {
					r.logger.Error("failed to close session log file", zap.Error(err))
					return
				}

				uploadBuffer := make([]byte, buf.Len())
				copy(uploadBuffer, buf.Bytes())
				buf.Reset()
				r.zipWriter = gzip.NewWriter(&buf)

				go func(uploadBuffer []byte) {
					if err := r.api.UploadRecording(uploadBuffer, r.sessionKey, r.recordingID.String()); err != nil {
						r.logger.Error("failed to upload recording", zap.Error(err))
						return
					}
				}(uploadBuffer)
			}
		}

		if err := r.zipWriter.Flush(); err != nil {
			r.logger.Error("failed to flush session log file", zap.Error(err))
			return
		}

		if err := r.zipWriter.Close(); err != nil {
			r.logger.Error("failed to close session log file", zap.Error(err))
			return
		}

		uploadBuffer := buf.Bytes()
		if err := r.api.UploadRecording(uploadBuffer, r.sessionKey, r.recordingID.String()); err != nil {
			r.logger.Error("failed to upload recording", zap.Error(err))
			return
		}
	}()

	return nil
}
