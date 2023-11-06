package sqlauthproxy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-go/lib/types/pointer"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	pe "github.com/pingcap/errors"
	"github.com/pkg/errors"
	"github.com/xwb1989/sqlparser"
	"go.uber.org/zap"
)

type mysqlLocalHandler struct {
	logger          *zap.Logger
	metadata        *border0.E2EEncryptionMetadata
	border0API      border0.Border0API
	socket          models.Socket
	database        string
	clientConn      *client.Conn
	serverConn      *server.Conn
	statements      map[int64]*client.Stmt
	preparedQueries map[int64]string
	lastAuth        time.Time
	recordingChan   chan message
}

var _ server.Handler = &mysqlLocalHandler{}

func (h *mysqlLocalHandler) Database() string {
	return h.database
}

func (h *mysqlLocalHandler) HandleConnection(serverConn *server.Conn, clientConn *client.Conn) {
	h.clientConn = clientConn
	h.serverConn = serverConn

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
		err := serverConn.HandleCommand()
		if err != nil {
			if errors.As(err, &mysql.ErrBadConn) {
				return
			}

			h.logger.Error("connection closed", zap.Error(err))
			return
		}
	}
}

func (h *mysqlLocalHandler) UseDB(dbName string) error {
	start := time.Now()
	var err error

	defer func() {
		var pErr *string
		if err != nil {
			pErr = pointer.To(err.Error())
		}

		h.record(fmt.Sprintf("use %s", dbName), nil, pErr, time.Since(start).Milliseconds(), nil, nil)
	}()

	err = h.clientConn.UseDB(dbName)
	if err == nil {
		h.database = dbName
	}

	return pe.Unwrap(err)
}

func (h *mysqlLocalHandler) HandleFieldList(table string, fieldWildcard string) ([]*mysql.Field, error) {
	fields, err := h.clientConn.FieldList(table, fieldWildcard)
	if err != nil {
		return nil, errors.Wrap(err, "failed to HandleFieldList")
	}

	return fields, nil
}

func (h *mysqlLocalHandler) HandleOtherCommand(cmd byte, data []byte) error {
	return mysql.NewError(
		mysql.ER_UNKNOWN_ERROR,
		fmt.Sprintf("command %d is not supported", cmd),
	)
}

func (h *mysqlLocalHandler) HandleQuery(query string) (*mysql.Result, error) {
	start := time.Now()
	var err error
	var affectedRows *uint64
	var rows *int64
	var status *uint16

	defer func() {
		var pErr *string
		if err != nil {
			pErr = pointer.To(err.Error())
		}

		h.record(query, status, pErr, time.Since(start).Milliseconds(), rows, affectedRows)
	}()

	// check action allowed
	allowed, err := h.isAllowed(sqlparser.StmtType(sqlparser.Preview(query)))
	if err != nil {
		return nil, fmt.Errorf("failed to authorize: %w", err)
	}

	if !allowed {
		err = errors.New("command denied by policy")

		return nil, mysql.NewError(
			mysql.ER_ACCESS_DENIED_ERROR,
			fmt.Sprintf("command %s is not allowed", sqlparser.StmtType(sqlparser.Preview(query))),
		)
	}

	stmt, _ := sqlparser.Parse(query)

	start = time.Now()
	var res *mysql.Result
	switch stmt.(type) {
	case *sqlparser.Select:
		var streamRows int64
		res = &mysql.Result{}
		err = h.clientConn.ExecuteSelectStreaming(query, res,
			// called per row within result
			func(row []mysql.FieldValue) error {
				streamRows++
				return h.serverConn.WriteValue(row)
			},
			// called per result
			func(r *mysql.Result) error {
				return h.serverConn.WriteValue(r)
			},
		)

		status = pointer.To(res.Status)
		rows = pointer.To(streamRows)
		affectedRows = &res.AffectedRows

		return res, err
	default:
		res, err = h.clientConn.Execute(query)
		if res != nil {
			affectedRows = &res.AffectedRows
			r := int64(res.RowNumber())
			rows = pointer.To(r)
			status = pointer.To(res.Status)
		}

		return res, err
	}
}

func (h *mysqlLocalHandler) HandleStmtClose(context interface{}) error {
	intContext, ok := context.(int64)
	if !ok {
		return nil
	}

	stmt, ok := h.statements[intContext]
	if !ok {
		return nil
	}

	if err := stmt.Close(); err != nil {
		return errors.Wrap(err, "failed to HandleStmtClose")
	}

	if _, ok = h.preparedQueries[intContext]; ok {
		delete(h.preparedQueries, intContext)
	}

	return nil
}

func (h *mysqlLocalHandler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*mysql.Result, error) {
	start := time.Now()
	var err error
	queryStr := fmt.Sprintf("%s with args %v", query, args)
	var affectedRows *uint64
	var status *uint16
	var rows *int64

	defer func() {
		var pErr *string
		if err != nil {
			pErr = pointer.To(err.Error())
		}

		h.record(queryStr, status, pErr, time.Since(start).Milliseconds(), rows, affectedRows)
	}()

	intContext, ok := context.(int64)
	if !ok {
		return nil, fmt.Errorf("invalid context")
	}

	// check action allowed
	allowed, err := h.isAllowed(sqlparser.StmtType(sqlparser.Preview(query)))
	if err != nil {
		return nil, fmt.Errorf("failed to authorize: %w", err)
	}

	if !allowed {
		// h.proxySession.LogAudit(fmt.Sprintf("Query denied by policy: %s", query))
		err = errors.New("command denied by policy")
		return nil, mysql.NewError(
			mysql.ER_ACCESS_DENIED_ERROR,
			fmt.Sprintf("command %s is not allowed", sqlparser.StmtType(sqlparser.Preview(query))),
		)
	}

	if stmt, ok := h.statements[intContext]; !ok {
		inlineStmt, err := h.clientConn.Prepare(query)

		if err != nil {
			err = errors.Wrap(err, "failed to prepare")
			return nil, err
		}

		res, err := inlineStmt.Execute(args...)
		if res != nil {
			affectedRows = &res.AffectedRows
			r := int64(res.RowNumber())
			rows = pointer.To(r)
			status = pointer.To(res.Status)
		}

		return res, err
	} else {
		var query string
		if query, ok = h.preparedQueries[intContext]; !ok {
			return nil, fmt.Errorf("no such query")
		}

		queryStr = fmt.Sprintf("%s with args %v", query, args)
		res, err := stmt.Execute(args...)
		if res != nil {
			affectedRows = &res.AffectedRows
			r := int64(res.RowNumber())
			rows = pointer.To(r)
			status = pointer.To(res.Status)
		}

		return res, err
	}
}

func (h *mysqlLocalHandler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	// check action allowed
	allowed, err := h.isAllowed(sqlparser.StmtType(sqlparser.Preview(query)))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("failed to authorize: %w", err)
	}

	if !allowed {
		h.record(query, nil, pointer.To("command denied by policy"), 0, nil, nil)
		return 0, 0, nil, mysql.NewError(
			mysql.ER_ACCESS_DENIED_ERROR,
			fmt.Sprintf("command %s is not allowed", sqlparser.StmtType(sqlparser.Preview(query))),
		)
	}

	stmt, err := h.clientConn.Prepare(query)

	if err != nil {
		return 0, 0, nil, errors.Wrap(err, "failed to HandleStmtPrepare")
	}

	id := time.Now().UnixNano()
	h.statements[id] = stmt
	h.preparedQueries[id] = query

	paramNum := stmt.ParamNum()
	colNum := stmt.ColumnNum()

	return paramNum, colNum, id, nil
}

func (h *mysqlLocalHandler) isAllowed(stmtType string) (bool, error) {
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

func (h *mysqlLocalHandler) record(command string, status *uint16, result *string, duration int64, rows *int64, affectedRows *uint64) {
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
