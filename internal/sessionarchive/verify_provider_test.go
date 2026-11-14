package sessionarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setFixtureProvider(t *testing.T, o ExportOptions, provider any, omit bool) {
	t.Helper()
	filename := nativeFixtureRollout(t, o.CodexHome, o.ThreadID)
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitN(contents, []byte{'\n'}, 2)
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	metadata := first["payload"].(map[string]any)
	if omit {
		delete(metadata, "model_provider")
	} else {
		metadata["model_provider"] = provider
	}
	firstLine, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, append(append(firstLine, '\n'), lines[1]...), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPreservesProviderIdentityWithoutCredentials(t *testing.T) {
	for _, provider := range []string{"openai", "archive_verify", "company.proxy", `custom"provider`} {
		t.Run(provider, func(t *testing.T) {
			o, _ := fixture(t, "legacy")
			setFixtureProvider(t, o, provider, false)
			m, err := Export(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			before, err := Digest(t.Context(), m.ArchivePath)
			if err != nil {
				t.Fatal(err)
			}
			binary, logPath := verificationFixtureBinary(t, m, verificationFault{}, true)
			t.Setenv("OPENAI_API_KEY", "must-not-inherit-account-credentials")
			t.Setenv("OPENAI_BASE_URL", "https://must-not-inherit.invalid/v1")
			verified, err := Verify(t.Context(), m.ArchivePath, VerifyOptions{CodexBinary: binary})
			if err != nil || !verified.VerifiedSelfContained {
				t.Fatalf("provider %q did not verify: %v", provider, err)
			}
			events := readVerificationLog(t, logPath)
			assertVerificationPasses(t, events, m, 2)
			for _, event := range events {
				if event.Type == "start" && event.Provider != provider {
					t.Fatalf("provider changed from %q to %q", provider, event.Provider)
				}
			}
			after, err := Digest(t.Context(), m.ArchivePath)
			if err != nil || after != before {
				t.Fatal("verification modified the original archive")
			}
			if _, err := os.Stat(o.ProjectPath); err != nil {
				t.Fatal("verification removed the original project")
			}
		})
	}
}

func TestVerificationProviderRejectsMalformedMetadataAndDefaultsLegacy(t *testing.T) {
	for _, value := range []any{"", strings.Repeat("x", 257), "bad\nprovider", "bad\x7fprovider", map[string]any{"injected": "value"}} {
		t.Run("invalid", func(t *testing.T) {
			o, _ := fixture(t, "legacy")
			setFixtureProvider(t, o, value, false)
			if _, err := verificationProvider(t.Context(), o.CodexHome, o.ThreadID); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("malformed provider accepted: %v", err)
			}
		})
	}
	o, _ := fixture(t, "legacy")
	setFixtureProvider(t, o, nil, true)
	if provider, err := verificationProvider(t.Context(), o.CodexHome, o.ThreadID); err != nil || provider != "openai" {
		t.Fatalf("legacy provider default: %q %v", provider, err)
	}
	if _, err := verificationProvider(context.Background(), o.CodexHome, "missing"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing parent metadata accepted")
	}
}

func TestVerificationConfigDoesNotOverrideBuiltInOpenAI(t *testing.T) {
	home := t.TempDir()
	if err := writeVerificationConfig(home, "openai"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[model_providers.") || !strings.Contains(string(data), "openai_base_url = \"http://127.0.0.1:1\"") {
		t.Fatal("built-in provider was overridden or its endpoint was not isolated")
	}
	for _, arg := range verificationProviderOverrides("openai") {
		if strings.HasPrefix(arg, "model_providers=") {
			t.Fatal("CLI overrides redefine the built-in provider")
		}
	}
	if err := writeVerificationConfig(home, "bad\n[evil]"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("provider metadata injected configuration")
	}
}
