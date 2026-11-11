package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/iaia/telegramgw/internal/codexadapter"
	"github.com/iaia/telegramgw/internal/protocol"
)

func (s *sessionActor) claudeMCP(ctx context.Context, client *codexadapter.Client, args string) (protocol.Result, error) {
	request, err := protocol.ParseClaudeMCPArgs(args)
	if err != nil {
		return protocol.Result{}, validationError("%s", protocol.ClaudeMCPUsage)
	}
	result, err := client.ClaudeMCP(ctx, s.session.ThreadID, request)
	if err != nil {
		return protocol.Result{}, err
	}
	lines := []string{"Claude Code MCP servers"}
	if request.Action != "status" && result.Warning == "" {
		lines = append(lines, fmt.Sprintf("MCP %s: %d server(s) acknowledged.", request.Action, len(result.Changed)))
	}
	if result.Warning != "" {
		lines = append(lines, result.Warning)
	}
	if result.StatusStale {
		lines = append(lines, "Last observed status (before changes):")
	}
	if len(result.Servers) == 0 {
		lines = append(lines, "No MCP servers are configured in this Claude session.")
	}
	needsAuth := false
	for _, server := range result.Servers {
		lines = append(lines, fmt.Sprintf("• %s — %s · %d tools", server.Name, server.Status, server.ToolCount))
		if request.Verbose {
			if server.Scope != "" {
				lines = append(lines, "  Scope: "+server.Scope)
			}
			if server.Version != "" {
				lines = append(lines, "  Version: "+server.Version)
			}
			for _, name := range server.Tools {
				lines = append(lines, "  • "+name)
			}
			if server.ToolCount > len(server.Tools) {
				lines = append(lines, fmt.Sprintf("  … %d more tools", server.ToolCount-len(server.Tools)))
			}
		}
		needsAuth = needsAuth || server.Status == "needs-auth"
	}
	lines = append(lines, "", protocol.ClaudeMCPUsage)
	if needsAuth {
		lines = append(lines, "Servers needing authentication require Claude Code’s native sign-in on the worker host; /mcp reconnect can retry after authentication.")
	}
	text := s.agent.redactor.Redact(strings.Join(lines, "\n"))
	if len(text) > 64<<10 {
		text = string([]rune(text)[:min(len([]rune(text)), 16000)]) + "\n… output truncated. Use /mcp without verbose for a summary."
	}
	return protocol.Result{Text: text}, nil
}
