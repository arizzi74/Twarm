package registry

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/iaia/telegramgw/internal/protocol"
)

func restartCallback(t *testing.T, env eventTestEnv, action string, origin string) string {
	t.Helper()
	in := telegramUpdate(env, 1)
	token, err := env.store.CreateCallback(t.Context(), Callback{Action: action, OriginDeliveryID: origin, BotID: in.BotID, UserID: in.UserID, ChatID: in.ChatID, TopicID: in.TopicID, SessionID: env.session, RuntimeID: env.runtime, Generation: 1, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestTelegramWorkerRestartRequiresConfirmationAndIsOneUse(t *testing.T) {
	env := newHistoryTestEnv(t)
	in := telegramUpdate(env, 2)
	in.Action = "restart_worker"
	response, err := env.store.AcceptTelegram(t.Context(), in)
	if err != nil || response.View != "worker_restart_confirm" || response.CommandID != "" || response.WorkerName != "event-worker" {
		t.Fatalf("response=%+v %v", response, err)
	}
	var origin string
	if err := env.store.pool.QueryRow(t.Context(), "SELECT delivery_id FROM telegram_deliveries WHERE json_extract(payload,'$.view')='worker_restart_confirm'").Scan(&origin); err != nil {
		t.Fatal(err)
	}
	token := restartCallback(t, env, "restart_worker_confirm", origin)
	cancel := restartCallback(t, env, "restart_worker_cancel", origin)
	in = telegramUpdate(env, 3)
	in.CallbackToken = token
	response, err = env.store.AcceptTelegram(t.Context(), in)
	if err != nil || response.CommandID == "" || response.View != "worker_restart_queued" {
		t.Fatalf("response=%+v %v", response, err)
	}
	var payload []byte
	if err := env.store.pool.QueryRow(t.Context(), "SELECT payload FROM commands WHERE command_id=$1", response.CommandID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var command protocol.Command
	if err := json.Unmarshal(payload, &command); err != nil {
		t.Fatal(err)
	}
	if command.Operation != protocol.RestartWorker || command.SessionID != env.session.String() || command.WorkerID != env.worker.String() || command.RuntimeID != env.runtime.String() || command.ExpiresAt.Sub(command.CreatedAt) > time.Minute+time.Second {
		t.Fatalf("command=%+v", command)
	}
	for i, cb := range []string{token, cancel} {
		in = telegramUpdate(env, int64(4+i))
		in.CallbackToken = cb
		result, err := env.store.AcceptTelegram(t.Context(), in)
		if err != nil || result.CommandID != "" || result.View != "error" {
			t.Fatalf("repeat=%+v %v", result, err)
		}
	}
}

func TestTelegramWorkerRestartCancelAndStaleSelectionsNeverRestart(t *testing.T) {
	for _, scenario := range []string{"cancel", "selection", "generation", "user", "chat", "topic", "bot", "expired", "offline", "arguments", "missing selection"} {
		t.Run(scenario, func(t *testing.T) {
			env := newHistoryTestEnv(t)
			in := telegramUpdate(env, 2)
			in.Action = "restart_worker"
			switch scenario {
			case "arguments":
				in.Text = "another-worker"
			case "missing selection":
				if _, err := env.store.pool.Exec(t.Context(), "DELETE FROM telegram_bindings"); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := env.store.AcceptTelegram(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "arguments" || scenario == "missing selection" {
				if prepared.View != "error" {
					t.Fatal(prepared)
				}
				return
			}
			var origin string
			if err := env.store.pool.QueryRow(t.Context(), "SELECT delivery_id FROM telegram_deliveries WHERE json_extract(payload,'$.view')='worker_restart_confirm'").Scan(&origin); err != nil {
				t.Fatal(err)
			}
			confirm := restartCallback(t, env, "restart_worker_confirm", origin)
			cancel := restartCallback(t, env, "restart_worker_cancel", origin)
			in = telegramUpdate(env, 3)
			in.CallbackToken = confirm
			switch scenario {
			case "cancel":
				in.CallbackToken = cancel
			case "selection":
				if _, err := env.store.pool.Exec(t.Context(), "DELETE FROM telegram_bindings"); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if _, err := env.store.pool.Exec(t.Context(), "UPDATE runtimes SET generation=2 WHERE runtime_id=$1", env.runtime); err != nil {
					t.Fatal(err)
				}
			case "user":
				in.UserID++
			case "chat":
				in.ChatID++
			case "topic":
				in.TopicID++
			case "bot":
				in.BotID = "another-bot"
			case "expired":
				if _, err := env.store.pool.Exec(t.Context(), "UPDATE telegram_callbacks SET expires_at='2000-01-01T00:00:00Z' WHERE token=$1", confirm); err != nil {
					t.Fatal(err)
				}
			case "offline":
				if _, err := env.store.pool.Exec(t.Context(), "UPDATE workers SET connectivity='offline' WHERE worker_id=$1", env.worker); err != nil {
					t.Fatal(err)
				}
			}
			result, err := env.store.AcceptTelegram(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if result.CommandID != "" {
				t.Fatal("invalid confirmation restarted worker")
			}
			if scenario == "cancel" {
				in = telegramUpdate(env, 4)
				in.CallbackToken = confirm
				result, err = env.store.AcceptTelegram(t.Context(), in)
				if err != nil || result.CommandID != "" || result.View != "error" {
					t.Fatalf("cancel sibling=%+v %v", result, err)
				}
			}
		})
	}
}

func TestWebUIWorkerRestartFreezesTargetAndDeduplicatesRequest(t *testing.T) {
	env := newHistoryTestEnv(t)
	expected, err := env.store.WorkerRestartTarget(t.Context(), env.session)
	if err != nil {
		t.Fatal(err)
	}
	request := uuid.New()
	id, err := env.store.QueueWebUIWorkerRestart(t.Context(), expected, request)
	if err != nil || id != request.String() {
		t.Fatal(id, err)
	}
	if id, err := env.store.QueueWebUIWorkerRestart(t.Context(), expected, request); err != nil || id != request.String() {
		t.Fatal(id, err)
	}
	wrong := expected
	wrong.WorkerID = uuid.NewString()
	if _, err := env.store.QueueWebUIWorkerRestart(t.Context(), wrong, request); err == nil {
		t.Fatal("rebound duplicate accepted")
	}
	if _, err := env.store.pool.Exec(t.Context(), "UPDATE runtimes SET generation=2 WHERE runtime_id=$1", env.runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.QueueWebUIWorkerRestart(t.Context(), expected, uuid.New()); err == nil {
		t.Fatal("stale target accepted")
	}
}
