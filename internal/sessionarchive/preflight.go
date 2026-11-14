package sessionarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Compatibility failures have bounded, public reasons. Native diagnostics and
// local paths must not be forwarded to a browser or Telegram.
const (
	SchemaChanged  = "archive_schema_changed"
	HistoryChanged = "archive_history_changed"
	RuntimeChanged = "archive_runtime_changed"
	ProbeFailed    = "archive_precheck_failed"
)

type CompatibilityError struct {
	Reason string
	cause  error // internal diagnostics, never included in the public message
}

func (e *CompatibilityError) Error() string {
	switch e.Reason {
	case SchemaChanged:
		return "Codex storage structures differ from supported archive layouts. The original data has been kept."
	case HistoryChanged:
		return "The conversation file format differs from supported archive layouts. The original data has been kept."
	case RuntimeChanged:
		return "The installed Codex executable differs from the running runtime or changed during verification. Restart the runtime and retry. The original data has been kept."
	default:
		return "The isolated Codex archive compatibility check did not succeed. The original data has been kept."
	}
}
func (e *CompatibilityError) Unwrap() error { return ErrUnsupported }

type PreflightOptions struct {
	CodexBinary  string
	CodexVersion string
	SQLiteHome   string
	Progress     func(string)
}

type compatibilityContextKey struct{}
type compatibilityProof struct {
	version      string
	binaryDigest string
	schemas      map[string]string
}

// Cache entries are never persisted or accepted from an archive. A new worker
// process (including every MACT upgrade) starts without any dynamic proofs.
// The key includes the complete executable contents, runtime version and all
// observed schemas, so replacing a binary or migrating a DB invalidates it.
type compatibilityCache struct {
	mu      sync.Mutex
	proofs  map[string]*compatibilityProof
	running map[string]chan struct{}
}

var archiveCompatibilityCache compatibilityCache
var stableNativeVersion = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)

// Preflight checks the live databases read-only, then proves the installed
// binary using synthetic legacy and paginated histories in a credential-free
// temporary home. Only known, exact schema profiles may pass. The returned
// context authorizes this exact version for the caller's archive/restore job;
// no process-wide version allowlist is changed.
func Preflight(ctx context.Context, o PreflightOptions) (context.Context, error) {
	return archiveCompatibilityCache.check(ctx, o, probeCompatibility)
}

func (cache *compatibilityCache) check(ctx context.Context, o PreflightOptions, probe func(context.Context, string, string) (*compatibilityProof, error)) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	version := nativeVersion(o.CodexVersion)
	if !stableNativeVersion.MatchString(version) {
		return ctx, &CompatibilityError{Reason: ProbeFailed}
	}
	binary := o.CodexBinary
	if binary == "" {
		binary = "codex"
	}
	binary, err := exec.LookPath(binary)
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err == nil {
		binary, err = filepath.Abs(binary)
	}
	if err != nil {
		return ctx, &CompatibilityError{Reason: RuntimeChanged}
	}
	digest, err := executableDigest(ctx, binary)
	if err != nil {
		return ctx, compatibilityFailure(ctx, RuntimeChanged)
	}
	schemas, err := inspectNativeSchemas(ctx, o.SQLiteHome)
	if err != nil {
		return ctx, compatibilityFailure(ctx, SchemaChanged)
	}
	if _, err := matchSchemaProfile(schemas); err != nil {
		return ctx, err
	}
	key := version + ":" + digest + ":" + schemaKey(schemas)
	for {
		cache.mu.Lock()
		if proof := cache.proofs[key]; proof != nil {
			cache.mu.Unlock()
			progress(o.Progress, "Archive compatibility pre-check passed (cached)")
			return context.WithValue(ctx, compatibilityContextKey{}, proof), nil
		}
		if done := cache.running[key]; done != nil {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx, ctx.Err()
			case <-done:
				// Storage and the executable may have changed while waiting.
				// Inspect both again rather than reusing the earlier cache key.
				return cache.check(ctx, o, probe)
			}
		}
		if cache.running == nil {
			cache.running = make(map[string]chan struct{})
		}
		done := make(chan struct{})
		cache.running[key] = done
		cache.mu.Unlock()
		progress(o.Progress, "Checking Codex archive compatibility in isolation")
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		proof, probeErr := probe(probeCtx, binary, version)
		cancel()
		if probeErr != nil && ctx.Err() == nil {
			var failure *CompatibilityError
			if !errors.As(probeErr, &failure) {
				probeErr = &CompatibilityError{Reason: ProbeFailed, cause: probeErr}
			}
		}
		if probeErr == nil && proof == nil {
			probeErr = &CompatibilityError{Reason: ProbeFailed}
		}
		if probeErr == nil {
			// Do not cache proof for a binary or live schema changed while the
			// isolated check was running. Cache hits also inspect them anew.
			after, hashErr := executableDigest(ctx, binary)
			live, schemaErr := inspectNativeSchemas(ctx, o.SQLiteHome)
			if hashErr != nil || after != digest || proof.binaryDigest != digest || proof.version != version {
				probeErr = compatibilityFailure(ctx, RuntimeChanged)
			} else if len(proof.schemas) != len(nativeDBs) || schemaErr != nil || schemaKey(live) != schemaKey(schemas) || !schemasMatch(schemas, proof.schemas) {
				probeErr = compatibilityFailure(ctx, SchemaChanged)
			}
		}
		cache.mu.Lock()
		if probeErr == nil {
			if cache.proofs == nil || len(cache.proofs) >= 16 {
				cache.proofs = make(map[string]*compatibilityProof)
			}
			cache.proofs[key] = proof
		}
		delete(cache.running, key)
		close(done)
		cache.mu.Unlock()
		if probeErr != nil {
			return ctx, probeErr
		}
		progress(o.Progress, "Archive compatibility pre-check passed")
		return context.WithValue(ctx, compatibilityContextKey{}, proof), nil
	}
}

func compatibilityFailure(ctx context.Context, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &CompatibilityError{Reason: reason}
}

func executableDigest(ctx context.Context, binary string) (string, error) {
	f, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return "", fmt.Errorf("Codex executable is not a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, &contextReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("Codex executable changed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Missing stores are allowed for an empty runtime. The isolated native check
// must still materialize a known state schema before granting a proof.
func inspectNativeSchemas(ctx context.Context, home string) (map[string]string, error) {
	if home == "" {
		return nil, fmt.Errorf("missing native database directory")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"logs_1.sqlite": true, "logs_2.sqlite": true, "thread_history_1.sqlite": true}
	for _, name := range nativeDBs {
		allowed[name] = true
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sqlite") && !allowed[entry.Name()] {
			return nil, &CompatibilityError{Reason: SchemaChanged}
		}
	}
	result := make(map[string]string)
	for _, name := range nativeDBs {
		file := filepath.Join(home, name)
		if _, err := os.Lstat(file); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		db, err := openDB(file, true)
		if err != nil {
			return nil, err
		}
		fingerprint, _, err := nativeSchemaFingerprint(ctx, db)
		db.Close()
		if err != nil {
			return nil, err
		}
		result[name] = fingerprint
	}
	return result, nil
}

func schemasMatch(observed, expected map[string]string) bool {
	for name, fingerprint := range observed {
		if fingerprint != expected[name] {
			return false
		}
	}
	return true
}

func matchSchemaProfile(observed map[string]string) (map[string]string, error) {
	for _, version := range []string{"0.165.0", "0.163.0"} {
		if schemas := nativeSchemaFingerprints[version]; schemasMatch(observed, schemas) {
			return schemas, nil
		}
	}
	return nil, &CompatibilityError{Reason: SchemaChanged}
}

func schemaKey(schemas map[string]string) string {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	var key strings.Builder
	for _, name := range names {
		key.WriteString(name + "=" + schemas[name] + ";")
	}
	return key.String()
}
