package claudeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/iaia/telegramgw/internal/protocol"
)

// Called under server.mu. Native control waits happen outside the lock so MCP
// administration cannot block unrelated sessions or the current model stream.
func (s *server) mcpRPC(peer *peer, msg rpcMessage, params params) {
	t := s.threads[params.ThreadID]
	if !canonicalID(params.ThreadID) || t == nil || s.deletions[params.ThreadID] != nil {
		rpcError(peer, msg.ID, -32602, "Claude session was not found or is being deleted")
		return
	}
	if params.MCP == nil || params.MCP.Validate() != nil || (msg.Method == "thread/mcp/status") != (params.MCP.Action == "status") {
		rpcError(peer, msg.ID, -32602, protocol.ClaudeMCPUsage)
		return
	}
	p := s.runners[t.ID]
	if p == nil {
		var err error
		p, err = s.startProcess(t)
		if err != nil {
			rpcError(peer, msg.ID, -32602, err.Error())
			return
		}
		s.runners[t.ID] = p
	}
	if p.mcpGate == nil {
		p.mcpGate = make(chan struct{}, 1)
	}
	request := *params.MCP
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		budget := min(s.cfg.RequestTimeout, 25*time.Second)
		ctx, cancel := context.WithTimeout(s.ctx, budget)
		defer cancel()
		// Peer disconnect fences a request that has not yet written any mutation.
		goDone := make(chan struct{})
		defer close(goDone)
		go func() {
			select {
			case <-peer.done:
				cancel()
			case <-ctx.Done():
			case <-goDone:
			}
		}()
		select {
		case <-p.ready:
		case <-ctx.Done():
			rpcError(peer, msg.ID, -32602, "Claude is still initializing. Retry /mcp once it is ready. No MCP change was submitted.")
			return
		case <-p.done:
			rpcError(peer, msg.ID, -32602, "Claude exited during MCP initialization. Retry /mcp.")
			return
		}
		select {
		case p.mcpGate <- struct{}{}:
			defer func() { <-p.mcpGate }()
		case <-ctx.Done():
			rpcError(peer, msg.ID, -32602, "Another MCP request is in progress. Retry /mcp to check status.")
			return
		}
		s.mu.Lock()
		valid := !s.closed && s.runners[t.ID] == p && s.threads[t.ID] == t && s.deletions[t.ID] == nil && p.initErr == nil
		s.mu.Unlock()
		if !valid || ctx.Err() != nil {
			rpcError(peer, msg.ID, -32602, "Claude session changed during MCP administration. Retry /mcp.")
			return
		}
		result, err := p.manageMCP(ctx, request)
		s.mu.Lock()
		valid = !s.closed && s.runners[t.ID] == p && s.threads[t.ID] == t && s.deletions[t.ID] == nil
		s.mu.Unlock()
		if !valid {
			// A change may have been written before the session disappeared.
			// Do not classify this as a pre-execution rejection.
			rpcError(peer, msg.ID, -32000, "Claude session changed during MCP administration. Run /mcp to check status before repeating any change.")
			return
		}
		if err != nil {
			rpcError(peer, msg.ID, -32602, err.Error())
			return
		}
		rpcResult(peer, msg.ID, result)
	}()
}

func (p *process) mcpStatus(ctx context.Context) ([]protocol.ClaudeMCPServer, error) {
	raw, err := p.mcpControl(ctx, map[string]any{"subtype": "mcp_status"})
	if err != nil {
		return nil, errors.New("Could not read native Claude MCP status. Check or update Claude Code, then retry /mcp.")
	}
	var response struct {
		Servers *[]struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Scope  string `json:"scope"`
			Info   struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Servers == nil || len(*response.Servers) > 200 {
		return nil, errors.New("Claude returned an invalid MCP status response. Update Claude Code and retry /mcp.")
	}
	servers := make([]protocol.ClaudeMCPServer, 0, len(*response.Servers))
	seen := map[string]bool{}
	for _, server := range *response.Servers {
		if !protocol.ValidClaudeMCPName(server.Name) || seen[server.Name] {
			return nil, errors.New("Claude returned an invalid MCP server name. Retry /mcp.")
		}
		seen[server.Name] = true
		status := server.Status
		switch status {
		case "connected", "failed", "needs-auth", "pending", "disabled":
		default:
			status = "unknown"
		}
		item := protocol.ClaudeMCPServer{Name: server.Name, Status: status, Scope: skillText(server.Scope, 80), Version: skillText(server.Info.Version, 80), ToolCount: len(server.Tools)}
		for _, tool := range server.Tools[:min(len(server.Tools), 100)] {
			item.Tools = append(item.Tools, skillText(tool.Name, 200))
		}
		servers = append(servers, item)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers, nil
}

func (p *process) manageMCP(ctx context.Context, request protocol.ClaudeMCPRequest) (protocol.ClaudeMCPResult, error) {
	servers, err := p.mcpStatus(ctx)
	result := protocol.ClaudeMCPResult{Servers: servers}
	if err != nil || request.Action == "status" {
		return result, err
	}
	found := request.Server == "all"
	for _, server := range servers {
		if request.Server != "all" && request.Server != server.Name {
			continue
		}
		found = true
		if request.Server == "all" && request.Action == "reconnect" && server.Status != "failed" && server.Status != "needs-auth" {
			continue
		}
		if request.Action == "enable" && server.Status != "disabled" || request.Action == "disable" && server.Status == "disabled" {
			continue
		}
		control := map[string]any{"subtype": "mcp_reconnect", "serverName": server.Name}
		if request.Action != "reconnect" {
			control["subtype"] = "mcp_toggle"
			control["enabled"] = request.Action == "enable"
		}
		if _, err := p.mcpControl(ctx, control); err != nil {
			result.StatusStale = true
			result.Warning = "The MCP change was not confirmed. Run /mcp to check current status before repeating it; it will not be retried automatically."
			return result, nil
		}
		result.Changed = append(result.Changed, server.Name)
	}
	if !found {
		return result, errors.New("MCP server not found. Use the exact name shown by /mcp.")
	}
	fresh, err := p.mcpStatus(ctx)
	if err != nil {
		result.StatusStale = true
		result.Warning = "MCP change acknowledged. Status refresh was unavailable; run /mcp to see current connections."
	} else {
		result.Servers = fresh
	}
	return result, nil
}

// Retry only local write-lock admission, before any request reaches Claude.
// A timed-out, written or rejected native mutation is never sent again.
func (p *process) mcpControl(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := p.controlRequest(ctx, request, false)
		if !errors.Is(err, errClaudeInputBusy) {
			return raw, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
