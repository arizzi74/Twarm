package codexadapter

import (
	"context"
	"github.com/iaia/telegramgw/internal/protocol"
)

func (c *Client) ClaudeMCP(ctx context.Context, threadID string, request protocol.ClaudeMCPRequest) (protocol.ClaudeMCPResult, error) {
	var result protocol.ClaudeMCPResult
	if err := request.Validate(); err != nil {
		return result, err
	}
	method := "thread/mcp/status"
	if request.Action != "status" {
		method = "thread/mcp/update"
	}
	err := c.request(ctx, method, map[string]any{"threadId": threadID, "mcp": request}, &result, false)
	return result, err
}
