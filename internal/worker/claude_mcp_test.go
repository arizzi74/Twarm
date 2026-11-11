package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iaia/telegramgw/internal/protocol"
)

func TestClaudeMCPCommandUsesScopedNativeControlsDuringActiveTurn(t *testing.T) {
	a, runtime, server, cleanup := testAgent(t)
	defer cleanup()
	a.cfg.AgentKind = "claude"
	session := installSession(a, runtime, "mcp-thread", "active-turn")
	actor := a.actorForThread(runtime.ID, session.ThreadID)
	client, _, _ := a.manager.Client(runtime.ID)
	status := protocol.ClaudeMCPResult{Servers: []protocol.ClaudeMCPServer{{Name: "playwright", Status: "connected", Scope: "project", Version: "1", ToolCount: 1, Tools: []string{"browser_click"}}}}
	if err := server.SetMethodResult("thread/mcp/status", status); err != nil {
		t.Fatal(err)
	}
	if err := server.SetMethodResult("thread/mcp/update", status); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{"", "verbose", "disable playwright", "reconnect all"} {
		result, err := actor.executeCodexCommand(t.Context(), client, "mcp", args)
		if err != nil || !strings.Contains(result.Text, "playwright — connected") || result.TurnID != "" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	if actor.session.ActiveTurnID != "active-turn" || hasCall(server.Calls(), "turn/start") || hasCall(server.Calls(), "mcpServerStatus/list") {
		t.Fatal("MCP used model prompt or Codex MCP API")
	}
	for _, call := range server.Calls() {
		if strings.HasPrefix(call.Method, "thread/mcp/") {
			var params struct {
				ThreadID string                    `json:"threadId"`
				MCP      protocol.ClaudeMCPRequest `json:"mcp"`
			}
			if err := json.Unmarshal(call.Params, &params); err != nil || params.ThreadID != session.ThreadID || params.MCP.Validate() != nil {
				t.Fatal("MCP scope lost", string(call.Params))
			}
		}
	}
	before := len(server.Calls())
	if _, err := actor.executeCodexCommand(t.Context(), client, "mcp", "add arbitrary-url"); err == nil {
		t.Fatal("unsupported MCP mutation accepted")
	}
	if len(server.Calls()) != before {
		t.Fatal("invalid command reached runtime")
	}
}

func TestClaudeMCPUpdateSafetyClassifiesReadsAndMutations(t *testing.T) {
	if !nativeReadOnlyRequest("thread/mcp/status") || nativeReadOnlyRequest("thread/mcp/update") {
		t.Fatal("mutation classified read-only")
	}
	for _, method := range []string{"thread/mcp/status", "thread/mcp/update"} {
		var activity nativeActivity
		activity.clientMessage([]byte(`{"id":1,"method":"` + method + `","params":{"threadId":"thread"}}`))
		if activity.idle() {
			t.Fatal("pending MCP request did not fence updates")
		}
		activity.serverMessage([]byte(`{"id":1,"result":{"servers":[]}}`))
		assertNativeIdle(t, &activity)
	}
}
