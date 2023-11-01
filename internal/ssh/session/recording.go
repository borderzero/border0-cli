package session

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	buf         bytes.Buffer
	uploadLock  sync.Mutex
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
	r.zipWriter = gzip.NewWriter(&r.buf)
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
		readBuffer := make([]byte, 1024)
		readResult := make(chan int, 1)

		go func() {
			for {
				n, err := r.reader.Read(readBuffer)
				if err != nil && err != io.ErrClosedPipe {
					r.logger.Error("failed to read buffer", zap.Error(err))
					close(readResult)
					return
				}

				if n == 0 {
					close(readResult)
					return
				}

				readResult <- n
			}
		}()

		for {
			select {
			case n, open := <-readResult:
				if !open {
					return
				}

				elapsed := time.Since(r.start).Seconds()
				message := []interface{}{
					float64(elapsed),
					string("o"),
					strings.ReplaceAll(string(readBuffer[:n]), "\n", "\r\n"),
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

func (r *Recording) Stop() error {
	if err := r.reader.Close(); err != nil {
		return fmt.Errorf("failed to close session log file: %s", err)
	}
	return nil
}

func (r *Recording) upload() error {
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

		if err := r.api.UploadRecording(uploadBuffer, r.sessionKey, r.recordingID.String()); err != nil {
			r.logger.Error("failed to upload recording", zap.Error(err))
			return
		}
	}(uploadBuffer)

	return nil
}
