package claudeadapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeSlowStartupWaitsAndSubmitsOnce(t *testing.T) {
	cfg := testConfig(t)
	cfg.StartupTimeout = 0
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_INIT_DELAY=300ms")
	r := startTest(t, cfg)
	if r.server.cfg.StartupTimeout != 2*time.Minute {
		t.Fatalf("startup allowance=%s", r.server.cfg.StartupTimeout)
	}
	if r.server.cfg.RequestTimeout != cfg.RequestTimeout {
		t.Fatal("ordinary request deadline changed")
	}
	thread := startThread(t, r)
	turn, err := r.StartTurn(context.Background(), thread.ID, "normal")
	if err != nil {
		t.Fatal(err)
	}
	r.server.mu.Lock()
	proc := r.server.runners[thread.ID]
	r.server.mu.Unlock()
	select {
	case <-proc.ready:
		t.Fatal("slow initialization was not pending")
	case <-time.After(80 * time.Millisecond):
	}
	r.server.mu.Lock()
	active := r.server.threads[thread.ID].active()
	submitted := proc.lifecycle.submitted
	r.server.mu.Unlock()
	if active == nil || active.ID != turn.ID || submitted {
		t.Fatal("prompt was submitted before initialization completed")
	}
	awaitEvent(t, r, "turn_completed")
	data, err := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"user":"normal"`); n != 1 {
		t.Fatalf("prompt submissions=%d", n)
	}
}

func TestClaudeStartupTimeoutReportsCauseAndAllowsFreshRetry(t *testing.T) {
	cfg := testConfig(t)
	cfg.StartupTimeout = 80 * time.Millisecond
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_HANG_INIT=1")
	r := startTest(t, cfg)
	thread := startThread(t, r)
	if _, err := r.StartTurn(context.Background(), thread.ID, "normal"); err != nil {
		t.Fatal(err)
	}
	event := awaitEvent(t, r, "turn_failed")
	if !strings.Contains(event.PublicErrorMessage, "initialization timed out after 80ms") || !strings.Contains(event.PublicErrorMessage, "prompt was not sent") {
		t.Fatalf("wrong startup error: %q", event.PublicErrorMessage)
	}
	r.server.mu.Lock()
	if r.server.runners[thread.ID] != nil {
		r.server.mu.Unlock()
		t.Fatal("failed process still owns the session")
	}
	for i, value := range r.server.cfg.Env {
		if value == "MACT_CLAUDE_HELPER_HANG_INIT=1" {
			r.server.cfg.Env[i] = "MACT_CLAUDE_HELPER_HANG_INIT=0"
		}
	}
	// The next attempt starts a new process with enough time for scheduler noise.
	r.server.cfg.StartupTimeout = time.Second
	r.server.mu.Unlock()
	if _, err := r.StartTurn(context.Background(), thread.ID, "normal"); err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, r, "turn_completed")
	data, err := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"user":"normal"`); n != 1 {
		t.Fatalf("failed attempt submitted input or retry duplicated it: %d", n)
	}
}

func TestClaudeStartupCanBeCancelledPromptly(t *testing.T) {
	cfg := testConfig(t)
	cfg.StartupTimeout = 0
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_HANG_INIT=1")
	r := startTest(t, cfg)
	thread := startThread(t, r)
	turn, err := r.StartTurn(context.Background(), thread.ID, "normal")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := r.Interrupt(context.Background(), thread.ID, turn.ID); err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, r, "turn_interrupted")
	if time.Since(started) > 2*time.Second {
		t.Fatal("cancel waited for startup deadline")
	}
	data, _ := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
	if strings.Contains(string(data), `"user":`) {
		t.Fatal("cancelled initialization submitted a prompt")
	}
}
