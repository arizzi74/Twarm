package worker

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/iaia/telegramgw/internal/protocol"
	"github.com/iaia/telegramgw/internal/workerrestart"
)

// This is worker maintenance, independent of a session actor or native process.
// Existing command-ledger recovery never replays an executing restart.
func (a *Agent) dispatchWorkerRestart(ctx context.Context, c protocol.Command, target protocol.Runtime) (protocol.CommandAck, error) {
	if target.ID != c.RuntimeID || target.Generation != c.RuntimeGeneration {
		return a.reject(c, protocol.StaleRuntime, "Runtime generation no longer matches.")
	}
	if time.Now().After(c.ExpiresAt) {
		return a.reject(c, protocol.CommandExpired, "Restart confirmation has expired.")
	}
	sessions, err := a.store.ListSessions(c.RuntimeID)
	if err != nil {
		return protocol.CommandAck{}, err
	}
	found := false
	for _, session := range sessions {
		if session.ID == c.SessionID && session.ThreadID == c.ThreadID && !session.Archived && !session.Deleted {
			found = true
			break
		}
	}
	if !found {
		return a.reject(c, protocol.UnknownSession, "Session is not available.")
	}
	if err := a.store.SetCommandState(c.ID, CommandExecuting, nil); err != nil {
		return protocol.CommandAck{}, err
	}
	schedule := a.restartWorker
	if schedule == nil {
		schedule = func(ctx context.Context, c protocol.Command) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			return workerrestart.Schedule(ctx, runtime.GOOS, executable, a.cfg.StateFile, c.ID, a.cfg.AgentKind, workerrestart.Run)
		}
	}
	if err := schedule(ctx, c); err != nil {
		return a.reject(c, protocol.UnsupportedOperation, "Could not schedule the worker restart. Check that its user service is installed, no update is running, and the worker is up to date.")
	}
	result := &protocol.Result{CommandID: c.ID, State: "completed", Text: "Worker restart scheduled. All sessions on this worker will disconnect briefly; active turns may be interrupted. Reconnect after the worker comes back online."}
	_, err = a.record(c, CommandCompleted, result, "command_completed")
	return protocol.CommandAck{CommandID: c.ID, Status: "accepted"}, err
}
