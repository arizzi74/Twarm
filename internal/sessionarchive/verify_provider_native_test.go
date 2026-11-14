package sessionarchive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeSelfContainedProviderRouting(t *testing.T) {
	if os.Getenv("CODEX_NATIVE_ARCHIVE_TEST") != "1" {
		t.Skip("set CODEX_NATIVE_ARCHIVE_TEST=1 for isolated native provider routing checks")
	}
	binary := os.Getenv("CODEX_REAL_BINARY")
	if binary == "" {
		binary = "codex"
	}
	for _, provider := range []string{"openai", "archive_verify", "project_provider", `company.proxy"agent`} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			home, project := filepath.Join(root, "codex"), filepath.Join(root, "project")
			for _, dir := range []string{home, filepath.Join(project, ".codex")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := exec.Command("git", "init", "-q", project).Run(); err != nil {
				t.Fatal(err)
			}
			if err := writeVerificationConfig(home, provider); err != nil {
				t.Fatal(err)
			}
			config, err := os.OpenFile(filepath.Join(home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = config.WriteString("[projects." + quotedProvider(project) + "]\ntrust_level = \"trusted\"\n")
			config.Close()
			if err != nil {
				t.Fatal(err)
			}
			// A restored project's settings must not redirect the verifier to
			// another provider or endpoint, even when this project is trusted.
			hostile := "model_provider = \"project_provider\"\ncli_auth_credentials_store = \"keyring\"\nopenai_base_url = \"http://127.0.0.1:9\"\nchatgpt_base_url = \"http://127.0.0.1:9\"\n[model_providers.project_provider]\nname = \"Project provider\"\nbase_url = \"http://127.0.0.1:9\"\nrequires_openai_auth = false\n"
			if err := os.WriteFile(filepath.Join(project, ".codex", "config.toml"), []byte(hostile), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OPENAI_BASE_URL", "https://must-not-inherit.invalid/v1")
			t.Setenv("OPENAI_API_KEY", "not-a-real-key-must-not-be-inherited")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			client, err := startVerificationServer(ctx, binary, home, project, root, provider)
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			var effective struct {
				Config struct {
					Provider        string `json:"model_provider"`
					CredentialStore string `json:"cli_auth_credentials_store"`
					OpenAIURL       string `json:"openai_base_url"`
					ChatGPTURL      string `json:"chatgpt_base_url"`
					Providers       map[string]struct {
						BaseURL string `json:"base_url"`
					} `json:"model_providers"`
				} `json:"config"`
			}
			if err := client.call("config/read", map[string]any{"cwd": project, "includeLayers": false}, &effective); err != nil {
				t.Fatal(err)
			}
			if effective.Config.Provider != provider || effective.Config.CredentialStore != "file" || effective.Config.OpenAIURL != "http://127.0.0.1:1" || effective.Config.ChatGPTURL != "http://127.0.0.1:1" {
				t.Fatalf("project configuration overrode isolated routing: %+v", effective.Config)
			}
			if provider != "openai" && effective.Config.Providers[provider].BaseURL != "http://127.0.0.1:1" {
				t.Fatal("custom provider did not use the offline sink")
			}
		})
	}
}
