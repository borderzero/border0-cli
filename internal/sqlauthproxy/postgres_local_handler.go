package sqlauthproxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgproto3/v2"
	"github.com/xwb1989/sqlparser"
	"go.uber.org/zap"
)

type postgresLocalHandler struct {
	logger          *zap.Logger
	metadata        *border0.E2EEncryptionMetadata
	border0API      border0.Border0API
	socket          models.Socket
	lastAuth        time.Time
	recordingChan   chan message
	clientConn      *pgconn.HijackedConn
	serverConn      net.Conn
	serverBackend   *pgproto3.Backend
	clientFrontend  *pgproto3.Frontend
	preparedQueries map[string]string
	binds           map[string]bind
	database        string
	queryStats      QueryStats
}

type QueryStats struct {
	query        string
	startTime    time.Time
	rowsReturned *int64
	rowsAffected *int64
	errorMessage *string
}

type bind struct {
	query  string
	params [][]byte
}

func (h *postgresLocalHandler) HandleConnection() {
	h.clientFrontend = pgproto3.NewFrontend(pgproto3.NewChunkReader(h.clientConn.Conn), h.clientConn.Conn)

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

	go func() {
		for {
			msg, err := h.clientFrontend.Receive()
			if err != nil {
				return
			}

			if err := h.handleServerMessage(msg); err != nil {
				return
			}
		}
	}()

	for {
		msg, err := h.serverBackend.Receive()
		if err != nil {
			return
		}

		if err := h.handleClientMessage(msg); err != nil {
			h.logger.Error("failed to handle message", zap.Error(err))
			return
		}
	}
}

func (h *postgresLocalHandler) handleServerMessage(msg pgproto3.BackendMessage) error {
	switch m := msg.(type) {
	case *pgproto3.CommandComplete:
		if h.queryStats.query != "" {
			commandTag := pgconn.CommandTag(m.CommandTag)
			rows := commandTag.RowsAffected()
			if commandTag.Select() {
				h.queryStats.rowsReturned = &rows
			} else if commandTag.Insert() || commandTag.Update() || commandTag.Delete() {
				h.queryStats.rowsAffected = &rows
			}
			h.record(h.queryStats.query, nil, nil, time.Since(h.queryStats.startTime).Milliseconds(), h.queryStats.rowsReturned, h.queryStats.rowsAffected)
		}
	case *pgproto3.ErrorResponse:
		h.queryStats.errorMessage = &m.Message
		h.record(h.queryStats.query, nil, h.queryStats.errorMessage, time.Since(h.queryStats.startTime).Milliseconds(), nil, nil)
	}
	return h.serverBackend.Send(msg)
}

func (h *postgresLocalHandler) handleClientMessage(msg pgproto3.FrontendMessage) error {
	switch m := msg.(type) {
	case *pgproto3.Terminate:
		h.clientFrontend.Send(msg)
		return nil
	case *pgproto3.Query:
		allowed, err := h.isAllowed(sqlparser.StmtType(sqlparser.Preview(m.String)))
		if err != nil {
			return fmt.Errorf("failed to authorize: %w", err)
		}

		if !allowed {
			result := "command denied by policy"
			h.record(m.String, nil, &result, 0, nil, nil)
			if err := h.serverBackend.Send(&pgproto3.ErrorResponse{
				Severity: "ERROR",
				Code:     "28000",
				Message:  "User not authorized",
				Detail:   fmt.Sprintf("User \"%s\" is not allowed to execute this command.", h.metadata.UserEmail),
			}); err != nil {
				return err
			}

			return h.serverBackend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		}

		h.queryStats = QueryStats{
			query:        m.String,
			startTime:    time.Now(),
			rowsReturned: nil,
			rowsAffected: nil,
			errorMessage: nil,
		}

		return h.clientFrontend.Send(m)
	case *pgproto3.Parse:
		h.preparedQueries[m.Name] = m.Query
		return h.clientFrontend.Send(msg)
	case *pgproto3.Bind:
		if _, ok := h.preparedQueries[m.PreparedStatement]; !ok {
			if err := h.serverBackend.Send(&pgproto3.ErrorResponse{
				Severity: "ERROR",
				Code:     "26000",
				Message:  "undefined prepared statement",
				Detail:   fmt.Sprintf("There is no prepared statement with name \"%s\" available.", m.PreparedStatement),
			}); err != nil {
				return err
			}

			return h.serverBackend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		}

		h.binds[m.DestinationPortal] = bind{
			query:  h.preparedQueries[m.PreparedStatement],
			params: m.Parameters,
		}

		return h.clientFrontend.Send(m)
	case *pgproto3.Describe, *pgproto3.Sync:
		return h.clientFrontend.Send(msg)
	case *pgproto3.Execute:
		var bind bind
		var ok bool

		if bind, ok = h.binds[m.Portal]; !ok {
			if err := h.serverBackend.Send(&pgproto3.ErrorResponse{
				Severity: "ERROR",
				Code:     "26000",
				Message:  "undefined prepared statement",
				Detail:   fmt.Sprintf("There is no prepared statement with name \"%s\" available.", m.Portal),
			}); err != nil {
				return err
			}

			return h.serverBackend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		}

		allowed, err := h.isAllowed(sqlparser.StmtType(sqlparser.Preview(bind.query)))
		if err != nil {
			return fmt.Errorf("failed to authorize: %w", err)
		}

		if !allowed {
			result := "command denied by policy"
			h.record(bind.query, nil, &result, 0, nil, nil)
			if err := h.serverBackend.Send(&pgproto3.ErrorResponse{
				Severity: "ERROR",
				Code:     "28000",
				Message:  "User not authorized",
				Detail:   fmt.Sprintf("User \"%s\" is not allowed to execute this command.", h.metadata.UserEmail),
			}); err != nil {
				return err
			}

			return h.serverBackend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		}

		h.queryStats = QueryStats{
			query:        fmt.Sprintf("%s with args %v", bind.query, bind.params),
			startTime:    time.Now(),
			rowsReturned: nil,
			rowsAffected: nil,
			errorMessage: nil,
		}

		return h.clientFrontend.Send(m)
	default:
		return h.serverBackend.Send(&pgproto3.ErrorResponse{
			Severity: "ERROR",
			Code:     "26000",
			Message:  "unsupported message type",
			Detail:   fmt.Sprintf("Message type %T is not supported", msg),
		})
	}
}

func (h *postgresLocalHandler) isAllowed(stmtType string) (bool, error) {
	if time.Since(h.lastAuth) > authTTL {
		actions, _, err := h.border0API.Evaluate(context.TODO(), &h.socket, h.metadata.ClientIP, h.metadata.UserEmail, h.metadata.SessionKey)
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

func (h *postgresLocalHandler) record(command string, status *uint16, result *string, duration int64, rows *int64, affectedRows *int64) {
	if !h.socket.RecordingEnabled {
		return
	}

	var affectedRowsUint64 *uint64
	if affectedRows != nil {
		affectedRowsUint64 = pointer.To(uint64(*affectedRows))

	}

	h.recordingChan <- message{
		Time:         time.Now().UnixMilli(),
		Database:     h.database,
		Command:      command,
		Status:       status,
		Result:       result,
		Duration:     duration,
		Rows:         rows,
		AffectedRows: affectedRowsUint64,
	}
}
