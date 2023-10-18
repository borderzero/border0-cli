package session

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// asciinema v2 header
// https://github.com/asciinema/asciinema/blob/develop/doc/asciicast-v2.md
type logHeader struct {
	Version   int    `json:"version"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Timestamp int64  `json:"timestamp"`
	Title     string `json:"title"`
}

type Recording struct {
	logger      *zap.Logger
	api         border0.Border0API
	sessionKey  string
	recordingID uuid.UUID
	reader      io.ReadCloser
	start       time.Time
	width       int
	height      int
	zipWriter   *gzip.Writer
}

func NewRecording(logger *zap.Logger, reader io.ReadCloser, sessionKey string, api border0.Border0API, width, height int) *Recording {
	return &Recording{
		logger:      logger,
		sessionKey:  sessionKey,
		api:         api,
		reader:      reader,
		recordingID: uuid.New(),
		width:       width,
		height:      height,
	}
}

func (r *Recording) Record() error {
	var buf bytes.Buffer
	r.zipWriter = gzip.NewWriter(&buf)
	r.start = time.Now()

	if r.width == 0 {
		r.width = 80
	}

	if r.height == 0 {
		r.height = 24
	}

	newloghdr := &logHeader{
		Version:   2,
		Width:     r.width,
		Height:    r.height,
		Timestamp: time.Now().Unix(),
		Title:     "Recorded SSH Logger",
	}

	headerJson, _ := json.Marshal(newloghdr)
	headerJson = append([]byte(headerJson), "\n"...)

	if _, err := r.zipWriter.Write(headerJson); err != nil {
		return fmt.Errorf("failed to write header to recording: %s", err)
	}

	go func() {
		readBuffer := make([]byte, 1024)

		for {
			n, err := r.reader.Read(readBuffer)
			if err != nil && err != io.ErrClosedPipe {
				r.logger.Error("failed to read buffer", zap.Error(err))
			}

			if n == 0 {
				break
			}

			elapsed := time.Since(r.start).Seconds()
			message := []interface{}{
				float64(elapsed),
				string("o"),
				strings.ReplaceAll(string(readBuffer[:n]), "\n", "\r\n"),
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

func (r *Recording) Stop() error {
	if err := r.reader.Close(); err != nil {
		return fmt.Errorf("failed to close session log file: %s", err)
	}
	return nil
}
