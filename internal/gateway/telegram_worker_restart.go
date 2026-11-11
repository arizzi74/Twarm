package gateway

import (
	"context"
	"fmt"

	"github.com/iaia/telegramgw/internal/registry"
)

func (s *Sender) renderWorkerRestart(ctx context.Context, row registry.Delivery, response registry.AcceptResult) (string, *TelegramKeyboard, error) {
	sessionID, err := requiredUUID("session", response.SessionID)
	if err != nil {
		return "", nil, err
	}
	runtimeID, err := requiredUUID("runtime", response.RuntimeID)
	if err != nil {
		return "", nil, err
	}
	keyboard := &TelegramKeyboard{}
	var buttons []TelegramButton
	for _, choice := range []struct{ action, label string }{{"restart_worker_confirm", "Restart worker"}, {"restart_worker_cancel", "Cancel"}} {
		token, err := s.callback(ctx, row, registry.Callback{Action: choice.action, UserID: response.UserID, SessionID: sessionID, RuntimeID: runtimeID, Generation: response.Generation})
		if err != nil {
			return "", nil, err
		}
		buttons = append(buttons, TelegramButton{Text: choice.label, Data: token})
	}
	keyboard.Rows = append(keyboard.Rows, buttons)
	return fmt.Sprintf("Restart worker %s?\n\nThis restarts the entire worker, affecting all its sessions. Active turns may be interrupted and connections will briefly disconnect. Project files and saved conversations are kept.", response.WorkerName), keyboard, nil
}
