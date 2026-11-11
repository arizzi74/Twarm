package claudeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iaia/telegramgw/internal/codexadapter"
	"github.com/iaia/telegramgw/internal/protocol"
)

const testMCPServers = `[{"name":"playwright","status":"connected","scope":"project","serverInfo":{"name":"Playwright","version":"1.2"},"tools":[{"name":"browser_click","description":"PRIVATE TOOL DESCRIPTION"}],"config":{"command":"PRIVATE EXECUTABLE","env":{"TOKEN":"PRIVATE MCP TOKEN"}}},{"name":"claude.ai Docs","status":"needs-auth","error":"PRIVATE MCP ERROR","config":{"url":"https://private.example?token=PRIVATE"}},{"name":"disabled","status":"disabled","tools":[]}]`

func TestClaudeMCPNativeStatusAndActionsNeverSubmitPrompts(t *testing.T) {
	cfg := testConfig(t)
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP="+testMCPServers)
	r := startTest(t, cfg)
	thread := startThread(t, r)
	call := func(action, server string) protocol.ClaudeMCPResult {
		t.Helper()
		result, err := r.ClaudeMCP(t.Context(), thread.ID, protocol.ClaudeMCPRequest{Action: action, Server: server})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := call("status", "")
	if len(result.Servers) != 3 || result.Servers[0].Name != "claude.ai Docs" || result.Servers[2].ToolCount != 1 || result.Servers[2].Tools[0] != "browser_click" {
		t.Fatalf("status=%+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "config") {
		t.Fatal("native credentials/config leaked")
	}
	result = call("disable", "playwright")
	if len(result.Changed) != 1 || result.Servers[2].Status != "disabled" {
		t.Fatal(result)
	}
	result = call("enable", "disabled")
	if len(result.Changed) != 1 || result.Servers[1].Status != "connected" {
		t.Fatal(result)
	}
	result = call("reconnect", "all")
	if len(result.Changed) != 1 || result.Changed[0] != "claude.ai Docs" {
		t.Fatal(result)
	}
	if _, err := r.ClaudeMCP(t.Context(), thread.ID, protocol.ClaudeMCPRequest{Action: "disable", Server: "missing"}); err == nil {
		t.Fatal("unknown server accepted")
	}
	data, _ := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
	if strings.Contains(string(data), `"user"`) {
		t.Fatal("MCP command became model input")
	}
	r.server.mu.Lock()
	defer r.server.mu.Unlock()
	if len(r.server.threads[thread.ID].Turns) != 0 || r.server.threads[thread.ID].NativeStarted {
		t.Fatal("MCP modified conversation lifecycle")
	}
}

func TestClaudeMCPDuringActiveTurnAndAcrossSessions(t *testing.T) {
	cfg := testConfig(t)
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP="+testMCPServers)
	r := startTest(t, cfg)
	first := startThread(t, r)
	second := startThread(t, r)
	turn, err := r.StartTurn(t.Context(), first.ID, "block")
	if err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, r, "turn_started")
	_, err = r.ClaudeMCP(t.Context(), first.ID, protocol.ClaudeMCPRequest{Action: "disable", Server: "playwright"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ClaudeMCP(t.Context(), second.ID, protocol.ClaudeMCPRequest{Action: "status"})
	if err != nil || result.Servers[2].Status != "connected" {
		t.Fatalf("session leak: %+v %v", result, err)
	}
	r.server.mu.Lock()
	active := r.server.threads[first.ID].active()
	unchanged := active != nil && active.ID == turn.ID
	r.server.mu.Unlock()
	if !unchanged {
		t.Fatal("MCP command replaced active turn")
	}
}

func TestClaudeMCPErrorsAreSafeAndDoNotKillNativeSession(t *testing.T) {
	for _, mode := range []string{"error", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.RequestTimeout = 200 * time.Millisecond
			cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP="+testMCPServers)
			if mode == "error" {
				cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP_ERROR=mcp_toggle")
			} else {
				cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP_HANG=mcp_toggle")
			}
			r := startTest(t, cfg)
			thread := startThread(t, r)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result, err := r.ClaudeMCP(ctx, thread.ID, protocol.ClaudeMCPRequest{Action: "disable", Server: "playwright"})
			if err != nil || result.Warning == "" || !result.StatusStale || strings.Contains(result.Warning, "PRIVATE") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if _, err := r.StartTurn(t.Context(), thread.ID, "normal"); err != nil {
				t.Fatal(err)
			}
			awaitEvent(t, r, "turn_completed")
			data, _ := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
			if strings.Count(string(data), `"mcp":"mcp_toggle"`) != 1 {
				t.Fatal("mutation retried")
			}
		})
	}
}

func TestClaudeMCPInitializationTimeoutDoesNotApplyLateChanges(t *testing.T) {
	cfg := testConfig(t)
	cfg.RequestTimeout = 50 * time.Millisecond
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_INIT_DELAY=200ms", "MACT_CLAUDE_HELPER_MCP="+testMCPServers)
	r := startTest(t, cfg)
	thread := startThread(t, r)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := r.ClaudeMCP(ctx, thread.ID, protocol.ClaudeMCPRequest{Action: "disable", Server: "playwright"})
	if err == nil || !strings.Contains(err.Error(), "still initializing") {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	data, _ := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
	if strings.Contains(string(data), `"mcp_toggle"`) {
		t.Fatal("expired request applied after initialization")
	}
}

func TestClaudeMCPReadRouteRejectsMutationBeforeStartingProcess(t *testing.T) {
	r := startTest(t, testConfig(t))
	thread := startThread(t, r)
	peer := &peer{server: r.server, queue: make(chan []byte, 1), done: make(chan struct{}), initialized: true}
	r.server.receive(peer, []byte(`{"id":9,"method":"thread/mcp/status","params":{"threadId":"`+thread.ID+`","mcp":{"action":"disable","server":"playwright"}}}`))
	select {
	case raw := <-peer.queue:
		if !strings.Contains(string(raw), `"error"`) {
			t.Fatal(string(raw))
		}
	case <-time.After(time.Second):
		t.Fatal("missing rejection")
	}
	r.server.mu.Lock()
	defer r.server.mu.Unlock()
	if len(r.server.runners) != 0 {
		t.Fatal("read-only route initialized mutable request")
	}
}

func TestClaudeMCPSessionChangeAfterWriteIsNotPreExecutionRejection(t *testing.T) {
	cfg := testConfig(t)
	cfg.RequestTimeout = time.Second
	cfg.Env = append(cfg.Env, "MACT_CLAUDE_HELPER_MCP="+testMCPServers, "MACT_CLAUDE_HELPER_MCP_HANG=mcp_toggle")
	r := startTest(t, cfg)
	thread := startThread(t, r)
	done := make(chan error, 1)
	go func() {
		_, err := r.ClaudeMCP(t.Context(), thread.ID, protocol.ClaudeMCPRequest{Action: "disable", Server: "playwright"})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(cfg.WorkingDirectory, "actions.jsonl"))
		if strings.Contains(string(data), `"mcp":"mcp_toggle"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MCP change was not written")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.server.mu.Lock()
	proc := r.server.runners[thread.ID]
	delete(r.server.runners, thread.ID)
	r.server.mu.Unlock()
	proc.stop()
	var rpc *codexadapter.RPCError
	select {
	case err := <-done:
		if !errors.As(err, &rpc) || rpc.Code != -32000 {
			t.Fatalf("partial request treated as rejection: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not settle")
	}
}
