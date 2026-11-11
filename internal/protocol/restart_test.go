package protocol

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestRestartWorkerRequiresFrozenSessionAndNoAgentInput(t *testing.T) {
	c := Command{ID: uuid.NewString(), WorkerID: uuid.NewString(), RuntimeID: uuid.NewString(), RuntimeGeneration: 1, SessionID: uuid.NewString(), ThreadID: "thread", Operation: RestartWorker, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"session", "thread", "generation", "text", "rpc", "turn"} {
		t.Run(scenario, func(t *testing.T) {
			c := c
			switch scenario {
			case "session":
				c.SessionID = ""
			case "thread":
				c.ThreadID = ""
			case "generation":
				c.RuntimeGeneration = 0
			case "text":
				c.Arguments.Text = "sudo reboot"
			case "rpc":
				c.Arguments.Codex = &CodexCommandPayload{Name: "restart"}
			case "turn":
				c.ExpectedTurnID = "turn"
			}
			if c.Validate() == nil {
				t.Fatal("invalid restart accepted")
			}
		})
	}
}
