package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iaia/telegramgw/internal/registry"
)

func TestWorkerRestartConfirmationNamesWorkerAndFreezesCallbacks(t *testing.T) {
	store := renderFixture()
	sender := NewSender(store, nil, nil, SenderOptions{BotID: "bot", OwnerID: 42})
	payload, _ := json.Marshal(registry.AcceptResult{View: "worker_restart_confirm", UserID: 77, WorkerName: "raspberrypi", SessionID: testSessionID.String(), RuntimeID: testRuntimeID.String(), Generation: 7})
	row := registry.Delivery{ID: "restart-confirm-delivery", Kind: "ui_response", Payload: payload, ChatID: 42, TopicID: 3}
	text, keyboard, err := sender.render(t.Context(), row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "raspberrypi") || !strings.Contains(text, "all its sessions") || !strings.Contains(text, "interrupted") || keyboard == nil || len(keyboard.Rows[0]) != 2 || len(store.callbacks) != 2 {
		t.Fatal(text, keyboard)
	}
	for _, callback := range store.callbacks {
		if callback.UserID != 77 || callback.RuntimeID != testRuntimeID || callback.SessionID != testSessionID || callback.Generation != 7 || callback.OriginDeliveryID != row.ID || callback.ChatID != 42 || callback.TopicID != 3 {
			t.Fatalf("lost scope: %+v", callback)
		}
	}
}
