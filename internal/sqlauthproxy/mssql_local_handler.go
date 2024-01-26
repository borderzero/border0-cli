package sqlauthproxy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	mssql "github.com/Microsoft/go-mssqldb"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/pkg/errors"
	"github.com/xwb1989/sqlparser"
	"go.uber.org/zap"
)

var reverseMap = map[int]string{
	1:  "sp_Cursor",
	2:  "sp_CursorOpen",
	3:  "sp_CursorPrepare",
	4:  "sp_CursorExecute",
	5:  "sp_CursorPrepExec",
	6:  "sp_CursorUnprepare",
	7:  "sp_CursorFetch",
	8:  "sp_CursorOption",
	9:  "sp_CursorClose",
	10: "sp_ExecuteSql",
	11: "sp_Prepare",
	13: "sp_PrepExec",
	14: "sp_PrepExecRpc",
	15: "sp_Unprepare",
}

type mssqlLocalHandler struct {
	logger         *zap.Logger
	metadata       *border0.E2EEncryptionMetadata
	border0API     border0.Border0API
	socket         models.Socket
	database       string
	upstreamConn   *mssql.Client
	downstreamConn *mssql.ServerSession
	lastAuth       time.Time
	recordingChan  chan message
}

func (h *mssqlLocalHandler) HandleConnection(ctx context.Context) {
	defer func() {
		if err := recover(); err != nil {
			h.logger.Error("panic", zap.Any("err", err))
		}
	}()

	if h.socket.RecordingEnabled {
		r, err := newRecording(h.logger, h.socket.SocketID, h.metadata.SessionKey, h.border0API)
		if err != nil {
			h.logger.Error("failed to record session", zap.Error(err))
			return
		}

		if err := r.Record(h.recordingChan); err != nil {
			h.logger.Error("failed to record session", zap.Error(err))
			return
		}

		defer close(h.recordingChan)
	}

	for {
		packetType, err := h.downstreamConn.ReadCommand()
		if err != nil {
			if err == io.EOF {
				return
			}

			h.logger.Error("failed to read command", zap.Error(err))
			return
		}

		switch packetType {
		case 1: // packSQLBatch:
			if err := h.handleSqlBatch(ctx); err != nil {
				h.logger.Error("failed to handle sql batch", zap.Error(err))
				return
			}
		case 3: // packRPC:
			if err := h.handleRPC(ctx); err != nil {
				h.logger.Error("failed to handle rpc", zap.Error(err))
				return
			}
		case 6: // packAttention:
			if err := h.handleAttention(ctx); err != nil {
				h.logger.Error("failed to handle attention", zap.Error(err))
				return
			}
		case 14: // packTransMgrReq:
			if err := h.handleTransMgrReq(ctx); err != nil {
				h.logger.Error("failed to handle attention", zap.Error(err))
				return
			}
		default:
			h.logger.Error("unknown packet type", zap.Int("packet_type", int(packetType)))
			return
		}
	}
}

func (h *mssqlLocalHandler) handleSqlBatch(ctx context.Context) error {
	start := time.Now()
	var derr error
	var affectedRows *uint64
	var rows *int64
	var status *uint16
	var query string

	defer func() {
		var pErr *string

		if derr != nil {
			pErr = pointer.To(derr.Error())
		}

		h.record(query, status, pErr, time.Since(start).Milliseconds(), rows, affectedRows)
	}()

	headers, query, err := h.downstreamConn.ParseSQLBatch()
	if err != nil {
		return err
	}

	// check action allowed
	allowed, err := h.isAllowed(ctx, sqlparser.StmtType(sqlparser.Preview(query)))
	if err != nil {
		return fmt.Errorf("failed to authorize: %w", err)
	}

	if !allowed {
		derr = errors.New("command denied by policy")
		return fmt.Errorf("command denied by policy")
	}

	res, err := h.upstreamConn.SendSqlBatch(ctx, h.downstreamConn, query, headers, false)
	if err != nil {
		return fmt.Errorf("failed to send sqlBatch: %w", err)
	}

	if h.upstreamConn.Database() != h.database {
		h.database = h.upstreamConn.Database()
	}

	for _, done := range res {
		if done.Status&0x10 != 0 {
			if done.CurCmd == 0xc1 {
				rows = pointer.To(int64(done.RowCount))
			} else {
				affectedRows = pointer.To(uint64(done.RowCount))
			}
		}

		derr = done.GetError()
		status = &done.Status
	}

	return nil
}

func (h *mssqlLocalHandler) handleRPC(ctx context.Context) error {
	start := time.Now()
	var derr error
	var affectedRows *uint64
	var rows *int64
	var status *uint16
	var query string

	defer func() {
		var pErr *string
		if derr != nil {
			pErr = pointer.To(derr.Error())
		}

		h.record(query, status, pErr, time.Since(start).Milliseconds(), rows, affectedRows)
	}()

	headers, proc, flags, params, values, err := h.downstreamConn.ParseRPC()
	if err != nil {
		return fmt.Errorf("failed to parse rpc: %w", err)
	}

	procName, ok := reverseMap[int(proc.Id())]
	if !ok {
		procName = proc.Name()
	}

	switch proc.Id() {
	case 10: // sp_ExecuteSql
		query, ok = values[0].(string)
		if !ok {
			return fmt.Errorf("failed to cast query to string")
		}
	default:
		query = procName
	}

	// check action allowed
	allowed, err := h.isAllowed(ctx, sqlparser.StmtType(sqlparser.Preview(query)))
	if err != nil {
		return fmt.Errorf("failed to authorize: %w", err)
	}

	if !allowed {
		derr = errors.New("command denied by policy")
		return fmt.Errorf("command denied by policy")
	}

	res, err := h.upstreamConn.SendRpc(ctx, h.downstreamConn, headers, proc, flags, params, false)
	if err != nil {
		return fmt.Errorf("failed to send rpc: %w", err)
	}

	if h.upstreamConn.Database() != h.database {
		h.database = h.upstreamConn.Database()
	}

	for _, done := range res {
		if done.Status&0x10 != 0 {
			if done.CurCmd == 0xc1 {
				rows = pointer.To(int64(done.RowCount))
			} else {
				affectedRows = pointer.To(uint64(done.RowCount))
			}
		}

		derr = done.GetError()
		status = &done.Status
	}

	return nil
}

func (h *mssqlLocalHandler) handleAttention(ctx context.Context) error {
	_, err := h.upstreamConn.SendAttention(ctx, h.downstreamConn)
	if err != nil {
		return fmt.Errorf("failed to send attention: %w", err)
	}

	return nil
}

func (h *mssqlLocalHandler) handleTransMgrReq(ctx context.Context) error {
	headers, rqtype, isolationLevel, name, newname, flags, err := h.downstreamConn.ParseTransMgrReq()
	if err != nil {
		return fmt.Errorf("failed to parse transMgrReq: %w", err)
	}

	_, err = h.upstreamConn.TransMgrReq(ctx, h.downstreamConn, headers, rqtype, isolationLevel, name, newname, flags, false)
	if err != nil {
		return fmt.Errorf("failed to send transMgrReq: %w", err)
	}

	return nil
}

func (h *mssqlLocalHandler) record(command string, status *uint16, result *string, duration int64, rows *int64, affectedRows *uint64) {
	if !h.socket.RecordingEnabled {
		return
	}

	h.recordingChan <- message{
		Time:         time.Now().UnixMilli(),
		Database:     h.database,
		Command:      command,
		Status:       status,
		Result:       result,
		Duration:     duration,
		Rows:         rows,
		AffectedRows: affectedRows,
	}
}

func (h *mssqlLocalHandler) isAllowed(ctx context.Context, stmtType string) (bool, error) {
	if time.Since(h.lastAuth) > authTTL {
		actions, _, err := h.border0API.Evaluate(ctx, &h.socket, h.metadata.ClientIP, h.metadata.UserEmail, h.metadata.SessionKey)
		if err != nil {
			return false, err
		}

		h.metadata.AllowedActions = actions
		h.lastAuth = time.Now()
	}

	for _, aa := range h.metadata.AllowedActions {
		if strings.EqualFold(aa, stmtType) || aa == "*" {
			return true, nil
		}
	}

	return false, nil
}
