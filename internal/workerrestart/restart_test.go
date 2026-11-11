package workerrestart

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestScheduleUsesIndependentSupervisorAndCorrectService(t *testing.T) {
	for _, system := range []string{"linux", "darwin"} {
		for _, kind := range []string{"codex", "claude"} {
			t.Run(system+"/"+kind, func(t *testing.T) {
				var calls [][]string
				run := func(_ context.Context, args ...string) ([]byte, error) {
					calls = append(calls, append([]string{}, args...))
					if system == "darwin" {
						return []byte("service {\n pid = " + strconv.Itoa(os.Getpid()) + "\n}"), nil
					}
					return []byte(strconv.Itoa(os.Getpid()) + "\n"), nil
				}
				state := filepath.Join(t.TempDir(), "worker.db")
				id := uuid.NewString()
				if err := Schedule(t.Context(), system, "/private/path/worker", state, id, kind, run); err != nil {
					t.Fatal(err)
				}
				name := kind + "-worker"
				if system == "linux" {
					if len(calls) != 2 || !reflect.DeepEqual(calls[0], []string{"systemctl", "--user", "show", name + ".service", "--property=MainPID", "--value"}) || calls[1][0] != "systemd-run" || calls[1][len(calls[1])-1] != "restart-service" {
						t.Fatalf("calls=%v", calls)
					}
					if strings.Contains(strings.Join(calls[1], " "), "Restart=") {
						t.Fatal("restart helper must not loop")
					}
				} else {
					data, err := os.ReadFile(filepath.Join(state+".restarts", id+".plist"))
					if err != nil {
						t.Fatal(err)
					}
					if err := xml.Unmarshal(data, new(any)); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(data), "<key>KeepAlive</key><false/>") || !strings.Contains(string(data), "<string>restart-service</string>") || strings.Contains(string(data), "/bin/sh") {
						t.Fatal(string(data))
					}
					if info, _ := os.Stat(filepath.Join(state+".restarts", id+".plist")); info.Mode().Perm() != 0600 {
						t.Fatal(info.Mode())
					}
				}
			})
		}
	}
}

func TestScheduleRejectsUnsafeOrMissingTargets(t *testing.T) {
	for _, scenario := range []string{"bad id", "relative exe", "relative state", "kind", "platform", "missing service", "supervisor failure"} {
		t.Run(scenario, func(t *testing.T) {
			system, exe, state, id, kind := "linux", "/bin/worker", filepath.Join(t.TempDir(), "worker.db"), uuid.NewString(), "claude"
			calls := 0
			run := func(_ context.Context, args ...string) ([]byte, error) {
				calls++
				if scenario == "missing service" || scenario == "supervisor failure" && calls == 2 {
					return nil, errors.New("failure")
				}
				return []byte(strconv.Itoa(os.Getpid())), nil
			}
			switch scenario {
			case "bad id":
				id = "../other"
			case "relative exe":
				exe = "worker"
			case "relative state":
				state = "state"
			case "kind":
				kind = "claude;sudo"
			case "platform":
				system = "windows"
			}
			if err := Schedule(t.Context(), system, exe, state, id, kind, run); err == nil {
				t.Fatal("invalid restart accepted")
			}
		})
	}
}

func TestRestartHelperCancellationNeverControlsService(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RunHelper(ctx, "linux", "claude", func(context.Context, ...string) ([]byte, error) {
		t.Fatal("cancelled helper restarted service")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRestartHelperUsesFixedService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, kind := range []string{"codex", "claude"} {
		var got []string
		if err := RunHelper(t.Context(), "linux", kind, func(_ context.Context, args ...string) ([]byte, error) { got = args; return nil, nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []string{"systemctl", "--user", "restart", kind + "-worker.service"}) {
			t.Fatal(got)
		}
	}
}

func TestScheduleNeverRestartsAnotherWorkerInTheSameAccount(t *testing.T) {
	for _, system := range []string{"linux", "darwin"} {
		t.Run(system, func(t *testing.T) {
			calls := 0
			run := func(context.Context, ...string) ([]byte, error) {
				calls++
				if system == "darwin" {
					return []byte("pid = 1"), nil
				}
				return []byte("1"), nil
			}
			if err := Schedule(t.Context(), system, "/private/worker", filepath.Join(t.TempDir(), "state"), uuid.NewString(), "claude", run); err == nil {
				t.Fatal("restart targeted a different service process")
			}
			if calls != 1 {
				t.Fatal("restart supervisor launched for a different worker")
			}
		})
	}
}
