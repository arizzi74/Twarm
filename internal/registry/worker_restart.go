package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iaia/telegramgw/internal/protocol"
)

func prepareTelegramWorkerRestart(ctx context.Context, tx *dbTx, in IncomingUpdate) (AcceptResult, error) {
	if strings.TrimSpace(in.Text) != "" || strings.TrimSpace(in.Target) != "" {
		return AcceptResult{View: "error", ErrorCode: "restart_worker_usage"}, nil
	}
	// Only the currently selected session, never a reply to an old message.
	in.ReplyToMessageID = 0
	target, err := resolveRoute(ctx, tx, in)
	if err != nil {
		return AcceptResult{}, err
	}
	snapshot, err := readWorkerRestartTarget(ctx, tx, target.sessionID)
	if err != nil {
		return AcceptResult{}, err
	}
	name := snapshot.WorkerName
	return AcceptResult{View: "worker_restart_confirm", UserID: in.UserID, SessionID: target.sessionID.String(), RuntimeID: target.runtimeID.String(), Generation: target.generation, WorkerName: name}, nil
}

func consumeWorkerRestart(ctx context.Context, tx *dbTx, in IncomingUpdate, action string, callback callbackPayload, sessionID *uuid.UUID) (AcceptResult, error) {
	if sessionID == nil || callback.RuntimeID == "" || callback.Generation <= 0 {
		return AcceptResult{}, ErrCallbackInvalid
	}
	// Retire both confirmation and cancel buttons atomically, preventing another
	// click on either sibling from producing a second request.
	if callback.OriginDeliveryID != "" {
		if _, err := tx.Exec(ctx, `UPDATE telegram_callbacks SET used_at=(strftime('%Y-%m-%dT%H:%M:%f','now') || '000000Z') WHERE action IN ('restart_worker_confirm','restart_worker_cancel') AND json_extract(payload,'$.origin_delivery_id')=$1 AND token<>$2 AND used_at IS NULL`, callback.OriginDeliveryID, in.CallbackToken); err != nil {
			return AcceptResult{}, err
		}
	}
	if action == "restart_worker_cancel" {
		return AcceptResult{View: "worker_restart_cancelled"}, nil
	}
	target, err := sessionRoute(ctx, tx, *sessionID)
	if err != nil {
		return AcceptResult{}, callbackTargetError(err)
	}
	if !callbackMatchesTarget(callback, target) {
		return AcceptResult{}, ErrCallbackInvalid
	}
	in.ReplyToMessageID = 0
	current, err := resolveRoute(ctx, tx, in)
	if err != nil {
		return AcceptResult{}, callbackTargetError(err)
	}
	if current.sessionID != target.sessionID {
		return AcceptResult{}, ErrCallbackInvalid
	}
	if _, err := readWorkerRestartTarget(ctx, tx, target.sessionID); err != nil {
		return AcceptResult{}, err
	}
	// A restart must not unexpectedly execute when a long-offline worker returns.
	in.CommandTTL = time.Minute
	command, err := createTelegramCommand(ctx, tx, in, target, protocol.RestartWorker, target.sessionID.String(), "", protocol.Arguments{})
	if err != nil {
		return AcceptResult{}, err
	}

	return AcceptResult{View: "worker_restart_queued", SessionID: target.sessionID.String(), RuntimeID: target.runtimeID.String(), CommandID: command.ID}, nil
}

// WorkerRestartTarget freezes the session's worker and runtime generation for
// a browser confirmation. It contains no runtime credentials or native prompt.
type WorkerRestartTarget struct {
	SessionID  string `json:"session_id"`
	WorkerID   string `json:"worker_id"`
	WorkerName string `json:"worker_name"`
	RuntimeID  string `json:"runtime_id"`
	Generation uint64 `json:"generation"`
}

type restartReader interface {
	QueryRow(context.Context, string, ...any) *dbRow
}

func readWorkerRestartTarget(ctx context.Context, db restartReader, sessionID uuid.UUID) (WorkerRestartTarget, error) {
	var target WorkerRestartTarget
	var online bool
	err := db.QueryRow(ctx, `SELECT session.session_id,session.worker_id,worker.name,session.runtime_id,runtime.generation,
 worker.enabled=TRUE AND worker.connectivity='online' FROM sessions session
 JOIN workers worker ON worker.worker_id=session.worker_id JOIN runtimes runtime ON runtime.runtime_id=session.runtime_id AND runtime.worker_id=session.worker_id
 WHERE session.session_id=$1 AND session.archived=FALSE`, sessionID).Scan(&target.SessionID, &target.WorkerID, &target.WorkerName, &target.RuntimeID, &target.Generation, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrTelegramTarget
	}
	if err != nil {
		return target, err
	}
	if !online || target.Generation == 0 {
		return target, ErrTelegramTarget
	}
	return target, nil
}

func (s *Store) WorkerRestartTarget(ctx context.Context, sessionID uuid.UUID) (WorkerRestartTarget, error) {
	return readWorkerRestartTarget(ctx, s.pool, sessionID)
}

func (s *Store) QueueWebUIWorkerRestart(ctx context.Context, expected WorkerRestartTarget, requestID uuid.UUID) (string, error) {
	sessionID, err := uuid.Parse(expected.SessionID)
	if err != nil || requestID == uuid.Nil {
		return "", ErrTelegramTarget
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var previous []byte
	err = tx.QueryRow(ctx, "SELECT payload FROM commands WHERE command_id=$1 AND source='webui' AND operation='restart_worker'", requestID).Scan(&previous)
	if err == nil {
		var command protocol.Command
		if json.Unmarshal(previous, &command) != nil || command.SessionID != expected.SessionID || command.WorkerID != expected.WorkerID || command.RuntimeID != expected.RuntimeID || command.RuntimeGeneration != expected.Generation {
			return "", ErrCallbackInvalid
		}
		return command.ID, tx.Commit(ctx)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	current, err := readWorkerRestartTarget(ctx, tx, sessionID)
	if err != nil {
		return "", err
	}
	if current.WorkerID != expected.WorkerID || current.RuntimeID != expected.RuntimeID || current.Generation != expected.Generation {
		return "", ErrCallbackInvalid
	}
	target, err := sessionRoute(ctx, tx, sessionID)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	command := protocol.Command{ID: requestID.String(), WorkerID: target.workerID.String(), RuntimeID: target.runtimeID.String(), RuntimeGeneration: uint64(target.generation), SessionID: sessionID.String(), ThreadID: target.threadID, Operation: protocol.RestartWorker, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := command.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	var collision bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM commands WHERE command_id=$1)", requestID).Scan(&collision); err != nil {
		return "", err
	}
	if collision {
		return "", ErrCallbackInvalid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO commands (command_id,source,worker_id,runtime_id,runtime_generation,session_id,operation,payload,status,expires_at) VALUES ($1,'webui',$2,$3,$4,$5,'restart_worker',$6,'pending',$7)`, requestID, target.workerID, target.runtimeID, target.generation, sessionID, string(payload), command.ExpiresAt); err != nil {
		return "", err
	}
	return command.ID, tx.Commit(ctx)
}
