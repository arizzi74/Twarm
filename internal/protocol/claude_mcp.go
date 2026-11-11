package protocol

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ClaudeMCPUsage = "Use /mcp [verbose], /mcp reconnect [server|all], /mcp enable <server|all>, or /mcp disable <server|all>."

type ClaudeMCPRequest struct {
	Action  string `json:"action"`
	Server  string `json:"server,omitempty"`
	Verbose bool   `json:"verbose,omitempty"`
}

func ValidClaudeMCPName(name string) bool {
	if name == "" || len(name) > 512 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (r ClaudeMCPRequest) Validate() error {
	switch r.Action {
	case "status":
		if r.Server == "" {
			return nil
		}
	case "reconnect", "enable", "disable":
		if !r.Verbose && ValidClaudeMCPName(r.Server) {
			return nil
		}
	}
	return errors.New(ClaudeMCPUsage)
}

func ParseClaudeMCPArgs(args string) (ClaudeMCPRequest, error) {
	r := ClaudeMCPRequest{Action: "status"}
	args = strings.TrimSpace(args)
	if args == "" || args == "verbose" {
		r.Verbose = args == "verbose"
		return r, nil
	}
	i := strings.IndexFunc(args, unicode.IsSpace)
	if i < 0 {
		r.Action = args
	} else {
		r.Action = args[:i]
		r.Server = strings.TrimSpace(args[i:])
	}
	if r.Action == "reconnect" && r.Server == "" {
		r.Server = "all"
	}
	return r, r.Validate()
}

// Deliberately excludes native config, URLs, environment, headers and errors.
type ClaudeMCPServer struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Scope     string   `json:"scope,omitempty"`
	Version   string   `json:"version,omitempty"`
	ToolCount int      `json:"tool_count"`
	Tools     []string `json:"tools,omitempty"`
}

type ClaudeMCPResult struct {
	StatusStale bool              `json:"status_stale,omitempty"`
	Servers     []ClaudeMCPServer `json:"servers"`
	Changed     []string          `json:"changed,omitempty"`
	Warning     string            `json:"warning,omitempty"`
}
