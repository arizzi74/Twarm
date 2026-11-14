package sessionarchive

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeSelfContainedPreflight(t *testing.T) {
	if os.Getenv("CODEX_NATIVE_ARCHIVE_TEST") != "1" {
		t.Skip("set CODEX_NATIVE_ARCHIVE_TEST=1 for an isolated installed-Codex compatibility check")
	}
	binary := os.Getenv("CODEX_REAL_BINARY")
	if binary == "" {
		binary = "codex"
	}
	expected := os.Getenv("CODEX_NATIVE_ARCHIVE_VERSION")
	if expected == "" {
		t.Fatal("CODEX_NATIVE_ARCHIVE_VERSION must identify the exact tested runtime")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	home := t.TempDir()
	o := PreflightOptions{CodexBinary: binary, CodexVersion: expected, SQLiteHome: home, Progress: func(message string) { t.Log(message) }}
	checked, err := Preflight(ctx, o)
	if err != nil {
		var failure *CompatibilityError
		if errors.As(err, &failure) {
			t.Fatalf("%v (isolated fixture: %v)", err, failure.cause)
		}
		t.Fatal(err)
	}
	if err := CheckVersionCompatibilityContext(checked, expected, expected); err != nil {
		t.Fatal(err)
	}
	if _, builtIn := nativeSchemaFingerprints[expected]; !builtIn {
		if err := CheckCompatibility(expected); !errors.Is(err, ErrUnsupported) {
			t.Fatal("request-scoped proof changed the process-wide allowlist")
		}
	}
	if err := CheckVersionCompatibilityContext(checked, "0.163.0", "0.165.0"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("preflight authorized cross-version restore")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight modified the real target store: %v %v", entries, err)
	}
	if _, err := Preflight(ctx, o); err != nil {
		t.Fatalf("cached proof: %v", err)
	}
	// The public context also permits a real archive/restore of a fabricated
	// conversation under this exact new version, without adding it to source.
	source, root := fixture(t, "legacy")
	source.CodexVersion = expected
	writeFixtureRollout(t, source.CodexHome, source.ProjectPath, source.ThreadID, "legacy", nil, expected)
	m, err := Export(checked, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(checked, RestoreOptions{ArchivePath: m.ArchivePath, CodexHome: filepath.Join(root, "restored-home"), Destination: filepath.Join(root, "restored-project"), CodexVersion: expected}); err != nil {
		t.Fatal(err)
	}
	// --version is checked in the same isolated environment as app-server;
	// this invocation is only a human-readable test assertion.
	output, err := exec.Command(binary, "--version").Output()
	if err != nil || nativeVersion(string(output)) != expected {
		t.Fatalf("tested binary changed: %q %v", output, err)
	}
}
