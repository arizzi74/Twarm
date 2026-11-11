package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/iaia/telegramgw/internal/protocol"
)

func TestWorkerRestartIsDurableDeduplicatedAndIndependentOfNativeTurn(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			a, runtime, server, cleanup := testAgent(t)
			defer cleanup()
			a.cfg.AgentKind = kind
			session := installSession(a, runtime, "restart-thread", "busy-turn")
			calls := 0
			a.restartWorker = func(_ context.Context, c protocol.Command) error {
				record, found, err := a.store.LoadCommand(c.ID)
				if err != nil || !found || record.State != CommandExecuting {
					t.Fatalf("restart not durable before scheduling: %+v %v", record, err)
				}
				calls++
				return nil
			}
			c := agentCommand(runtime, session, protocol.RestartWorker)
			c.ExpectedTurnID = ""
			c.Arguments = protocol.Arguments{}
			if ack, err := a.HandleCommand(t.Context(), c); err != nil || ack.Status != "accepted" {
				t.Fatalf("ack=%+v err=%v", ack, err)
			}
			if ack, err := a.HandleCommand(t.Context(), c); err != nil || ack.Status != "duplicate" {
				t.Fatalf("duplicate=%+v %v", ack, err)
			}
			if calls != 1 {
				t.Fatalf("scheduled %d restarts", calls)
			}
			record, _, _ := a.store.LoadCommand(c.ID)
			if record.State != CommandCompleted || record.Result == nil || !strings.Contains(record.Result.Text, "scheduled") {
				t.Fatalf("result=%+v", record)
			}
			if countCall(server.Calls(), "turn/start") != 0 || countCall(server.Calls(), "turn/interrupt") != 0 {
				t.Fatal("restart reached native agent")
			}
		})
	}
}

func TestWorkerRestartRejectsChangedTargetAndReportsSchedulingFailure(t *testing.T) {
	for _, scenario := range []string{"stale", "expired", "session", "thread", "supervisor"} {
		t.Run(scenario, func(t *testing.T) {
			a, runtime, _, cleanup := testAgent(t)
			defer cleanup()
			session := installSession(a, runtime, "restart-thread", "")
			c := agentCommand(runtime, session, protocol.RestartWorker)
			c.ExpectedTurnID = ""
			c.Arguments = protocol.Arguments{}
			calls := 0
			a.restartWorker = func(context.Context, protocol.Command) error { calls++; return errors.New("private host detail") }
			switch scenario {
			case "stale":
				c.RuntimeGeneration++
			case "expired":
				c.CreatedAt = time.Now().Add(-2 * time.Minute)
				c.ExpiresAt = time.Now().Add(-time.Minute)
			case "session":
				c.SessionID = "00000000-0000-4000-8000-000000000001"
			case "thread":
				c.ThreadID = "another"
			}
			ack, err := a.HandleCommand(t.Context(), c)
			if err != nil || ack.Error == nil {
				t.Fatalf("ack=%+v err=%v", ack, err)
			}
			want := 0
			if scenario == "supervisor" {
				want = 1
			}
			if calls != want {
				t.Fatal(calls)
			}
			if strings.Contains(ack.Error.Message, "private host detail") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestWorkerRestartStillWorksWhenNativeClientIsUnavailable(t *testing.T) {
	a, runtime, _, cleanup := testAgent(t)
	defer cleanup()
	session := installSession(a, runtime, "restart-thread", "")
	a.manager.mu.Lock()
	managed := a.manager.runtimes[runtime.ID]
	client := managed.client
	managed.client = nil
	a.manager.mu.Unlock()
	defer func() { a.manager.mu.Lock(); managed.client = client; a.manager.mu.Unlock() }()
	calls := 0
	a.restartWorker = func(context.Context, protocol.Command) error { calls++; return nil }
	c := agentCommand(runtime, session, protocol.RestartWorker)
	c.Arguments = protocol.Arguments{}
	c.ExpectedTurnID = ""
	ack, err := a.HandleCommand(t.Context(), c)
	if err != nil || ack.Status != "accepted" || calls != 1 {
		t.Fatalf("unavailable native client blocked restart: %+v %v calls=%d", ack, err, calls)
	}
}
