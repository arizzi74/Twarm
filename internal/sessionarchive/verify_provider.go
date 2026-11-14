package sessionarchive

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Read only the restored parent metadata. Provider identity is pinned by newer
// Codex versions; provider endpoints and credentials are never copied here.
func verificationProvider(ctx context.Context, home, threadID string) (string, error) {
	rollouts, err := scanRollouts(ctx, home)
	if err != nil {
		return "", err
	}
	r := rollouts[threadID]
	if r == nil {
		return "", fmt.Errorf("%w: restored parent metadata is missing", ErrUnsupported)
	}
	if err := readRolloutMetadata(ctx, r); err != nil {
		return "", err
	}
	f, err := openRollout(r.path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(&contextReader{ctx: ctx, r: f})
	scanner.Buffer(make([]byte, 4096), maxNativeLineBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%w: restored parent metadata is empty", ErrUnsupported)
	}
	var metadata struct {
		Payload struct {
			Provider *string `json:"model_provider"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &metadata); err != nil {
		return "", fmt.Errorf("%w: invalid restored provider metadata", ErrUnsupported)
	}
	// Legacy metadata may omit the provider; OpenAI is Codex's default.
	provider := "openai"
	if metadata.Payload.Provider != nil {
		provider = *metadata.Payload.Provider
	}
	if !validVerificationProvider(provider) {
		return "", fmt.Errorf("%w: invalid restored provider identity", ErrUnsupported)
	}
	return provider, nil
}

func validVerificationProvider(provider string) bool {
	return provider != "" && len(provider) <= 256 && utf8.ValidString(provider) && strings.IndexFunc(provider, unicode.IsControl) < 0
}

func quotedProvider(provider string) string {
	// JSON basic-string escapes are also valid in a TOML quoted key. Validate
	// controls separately and never interpolate an unquoted archive value.
	encoded, _ := json.Marshal(provider)
	return string(encoded)
}

func writeVerificationConfig(home, provider string) error {
	if !validVerificationProvider(provider) {
		return fmt.Errorf("%w: invalid verification provider identity", ErrUnsupported)
	}
	key := quotedProvider(provider)
	config := "model_provider = " + key + "\nmodel = \"archive-proof\"\ncheck_for_update_on_startup = false\ncli_auth_credentials_store = \"file\"\nopenai_base_url = \"http://127.0.0.1:1\"\nchatgpt_base_url = \"http://127.0.0.1:1\"\n"
	if provider != "openai" {
		config += "[model_providers." + key + "]\nname = \"Offline archive verification\"\nbase_url = \"http://127.0.0.1:1\"\nwire_api = \"responses\"\nrequires_openai_auth = false\nsupports_websockets = false\n"
	}
	return os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600)
}

func verificationProviderOverrides(provider string) []string {
	key := quotedProvider(provider)
	args := []string{
		"-c", "model_provider=" + key,
		"-c", "cli_auth_credentials_store=\"file\"",
		"-c", "openai_base_url=\"http://127.0.0.1:1\"",
		"-c", "chatgpt_base_url=\"http://127.0.0.1:1\"",
	}
	if provider != "openai" {
		args = append(args, "-c", "model_providers={"+key+"={name=\"Offline archive verification\",base_url=\"http://127.0.0.1:1\",wire_api=\"responses\",requires_openai_auth=false,supports_websockets=false}}")
	}
	return args
}
