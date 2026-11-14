package sessionarchive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const preflightUserSentinel = "MACT archive pre-check user history."
const preflightReplySentinel = "MACT archive pre-check assistant history."

// This proof creates only fabricated conversations. It never starts a model
// turn, reads the real Codex home, copies credentials or executes project code.
func probeCompatibility(ctx context.Context, binary, version string) (*compatibilityProof, error) {
	root, err := os.MkdirTemp("", "mact-archive-precheck-")
	if err != nil {
		return nil, compatibilityFailure(ctx, ProbeFailed)
	}
	defer os.RemoveAll(root)
	digest, err := executableDigest(ctx, binary)
	if err != nil {
		return nil, compatibilityFailure(ctx, RuntimeChanged)
	}
	proof := &compatibilityProof{version: version, binaryDigest: digest}
	// A provisional proof is private to this synthetic check. Its schemas are
	// assigned only after the native initialization matches a trusted profile;
	// it is never cached or returned unless both complete round trips pass.
	proofCtx := context.WithValue(ctx, compatibilityContextKey{}, proof)
	if err := verifyExecutableVersion(proofCtx, binary, version, root); err != nil {
		return nil, err
	}
	for _, mode := range []string{"legacy", "paginated"} {
		if err := probeHistoryMode(proofCtx, binary, root, mode, proof); err != nil {
			var precise *CompatibilityError
			if errors.As(err, &precise) {
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &CompatibilityError{Reason: ProbeFailed, cause: err}
		}
	}
	return proof, nil
}

func probeHistoryMode(ctx context.Context, binary, root, mode string, proof *compatibilityProof) error {
	modeRoot := filepath.Join(root, mode)
	home, project := filepath.Join(modeRoot, "codex"), filepath.Join(modeRoot, "project")
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	// Use the real built-in provider identity with a credential-free offline
	// endpoint. A custom-only fixture misses native provider-pinning rules.
	if err := writeVerificationConfig(home, "openai"); err != nil {
		return err
	}
	client, err := startVerificationServer(ctx, binary, home, project, modeRoot)
	if err != nil {
		return err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	err = client.call("thread/start", map[string]any{"cwd": project, "historyMode": mode, "modelProvider": "openai", "model": "archive-proof"}, &started)
	if err == nil {
		err = client.call("thread/name/set", map[string]any{"threadId": started.Thread.ID, "name": "MACT archive compatibility pre-check"}, nil)
	}
	client.close()
	if err != nil {
		return err
	}
	schemas, err := inspectNativeSchemas(ctx, home)
	if err != nil || schemas["state_5.sqlite"] == "" {
		return &CompatibilityError{Reason: SchemaChanged}
	}
	profile, err := matchSchemaProfile(schemas)
	if err != nil {
		return err
	}
	if proof.schemas != nil && !schemasMatch(schemas, proof.schemas) {
		return &CompatibilityError{Reason: SchemaChanged}
	}
	proof.schemas = profile
	all, err := scanRollouts(ctx, home)
	if err != nil || all[started.Thread.ID] == nil {
		return fmt.Errorf("native pre-check rollout unavailable")
	}
	if err := seedPreflightHistory(ctx, all[started.Thread.ID].path, started.Thread.ID, mode, proof.version); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "precheck.txt"), []byte("MACT archive project preservation check\n"), 0640); err != nil {
		return err
	}
	m, err := Export(ctx, ExportOptions{CodexHome: home, SQLiteHome: home, ThreadID: started.Thread.ID, ProjectPath: project, ArchiveDir: filepath.Join(modeRoot, "archives"), DisplayName: "Archive compatibility pre-check", CodexVersion: proof.version})
	if err != nil {
		return err
	}
	// Prove independence from the source. Both paths are private synthetic
	// directories, never supplied by a gateway request or archive manifest.
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	if err := os.RemoveAll(project); err != nil {
		return err
	}
	verified, err := Verify(ctx, m.ArchivePath, VerifyOptions{CodexBinary: binary, sentinels: []string{preflightUserSentinel, preflightReplySentinel}})
	if err != nil {
		return err
	}
	if !verified.VerifiedSelfContained {
		return fmt.Errorf("native pre-check was not verified")
	}
	return nil
}

func seedPreflightHistory(ctx context.Context, filename, id, mode, version string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(&contextReader{ctx: ctx, r: f})
	scanner.Buffer(make([]byte, 4096), maxNativeLineBytes)
	if !scanner.Scan() {
		f.Close()
		return fmt.Errorf("native pre-check metadata missing")
	}
	var first map[string]any
	err = json.Unmarshal(scanner.Bytes(), &first)
	f.Close()
	if err != nil {
		return err
	}
	metadata, ok := first["payload"].(map[string]any)
	if !ok || first["type"] != "session_meta" || metadata["id"] != id || metadata["cli_version"] != version || metadata["history_mode"] != mode {
		return fmt.Errorf("native conversation metadata changed")
	}
	if metadata["model_provider"] != "openai" {
		return fmt.Errorf("native pre-check did not preserve the OpenAI provider identity")
	}
	records := []map[string]any{
		first,
		{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "mact-precheck-turn", "model_context_window": 128000, "started_at": 1792324800}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": preflightUserSentinel}}}},
	}
	if mode == "legacy" {
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": preflightUserSentinel, "images": []any{}, "local_images": []any{}, "text_elements": []any{}}})
	}
	if mode == "paginated" {
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": id, "turn_id": "mact-precheck-turn", "item": map[string]any{"type": "UserMessage", "id": "mact-precheck-user", "content": []any{map[string]any{"type": "text", "text": preflightUserSentinel}}}, "completed_at_ms": 1792324800000}})
	}
	records = append(records, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": preflightReplySentinel}}, "phase": "final_answer"}})
	if mode == "legacy" {
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "agent_message", "message": preflightReplySentinel, "phase": "final_answer"}})
	} else {
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": id, "turn_id": "mact-precheck-turn", "item": map[string]any{"type": "AgentMessage", "id": "mact-precheck-reply", "content": []any{map[string]any{"type": "Text", "text": preflightReplySentinel}}, "phase": "final_answer"}, "completed_at_ms": 1792324801000}})
	}
	records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "mact-precheck-turn", "last_agent_message": preflightReplySentinel, "started_at": 1792324800, "completed_at": 1792324801}})
	var out bytes.Buffer
	for i, record := range records {
		record["timestamp"] = "2026-10-18T12:00:00Z"
		if mode == "paginated" {
			record["ordinal"] = i
		}
		if err := json.NewEncoder(&out).Encode(record); err != nil {
			return err
		}
	}
	return os.WriteFile(filename, out.Bytes(), 0600)
}
