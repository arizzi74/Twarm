package protocol

import "testing"

func TestClaudeMCPArguments(t *testing.T) {
	for _, args := range []string{"", "verbose", "reconnect", "reconnect all", "reconnect claude.ai Docs", "disable server", "enable all"} {
		if _, err := ParseClaudeMCPArgs(args); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, args := range []string{"bogus", "enable", "disable", "verbose all", "disable server\nother", "reconnect \x00", "enable all\tother"} {
		if _, err := ParseClaudeMCPArgs(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
