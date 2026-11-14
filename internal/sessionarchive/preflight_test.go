package sessionarchive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func preflightFixture(t *testing.T) (PreflightOptions, func(context.Context, string, string) (*compatibilityProof, error)) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	o := PreflightOptions{CodexBinary: binary, CodexVersion: "0.169.0", SQLiteHome: t.TempDir()}
	return o, func(ctx context.Context, binary, version string) (*compatibilityProof, error) {
		digest, err := executableDigest(ctx, binary)
		return &compatibilityProof{version: version, binaryDigest: digest, schemas: nativeSchemaFingerprints["0.165.0"]}, err
	}
}

func TestPreflightCachesOnlySuccessfulExactRuntimeProofs(t *testing.T) {
	o, success := preflightFixture(t)
	var cache compatibilityCache
	var calls int
	probe := func(ctx context.Context, binary, version string) (*compatibilityProof, error) {
		calls++
		return success(ctx, binary, version)
	}
	for i := 0; i < 2; i++ {
		checked, err := cache.check(t.Context(), o, probe)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckVersionCompatibilityContext(checked, o.CodexVersion, o.CodexVersion); err != nil {
			t.Fatal(err)
		}
		if err := CheckVersionCompatibilityContext(checked, "0.170.0", "0.170.0"); !errors.Is(err, ErrUnsupported) {
			t.Fatal("proof authorized another version")
		}
		if err := CheckVersionCompatibilityContext(checked, o.CodexVersion, "0.165.0"); !errors.Is(err, ErrUnsupported) {
			t.Fatal("matching schemas authorized cross-version import")
		}
	}
	if calls != 1 {
		t.Fatalf("unchanged runtime probed %d times", calls)
	}
	if err := CheckCompatibility(o.CodexVersion); !errors.Is(err, ErrUnsupported) {
		t.Fatal("cached proof escaped the request context")
	}
	if err := os.WriteFile(o.CodexBinary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.check(t.Context(), o, probe); err != nil || calls != 2 {
		t.Fatalf("executable replacement did not invalidate cache: calls=%d err=%v", calls, err)
	}
	o.CodexVersion = "0.170.0"
	if _, err := cache.check(t.Context(), o, probe); err != nil || calls != 3 {
		t.Fatalf("version change did not invalidate cache: calls=%d err=%v", calls, err)
	}
	// Every cache hit still inspects the live schemas. A new table must stop
	// the operation before any runtime probe, export or database write.
	db, err := openDB(filepath.Join(o.SQLiteHome, "state_5.sqlite"), false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE unexpected(value TEXT); INSERT INTO unexpected VALUES ('preserve')")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = cache.check(t.Context(), o, probe)
	var failure *CompatibilityError
	if !errors.As(err, &failure) || failure.Reason != SchemaChanged || calls != 3 {
		t.Fatalf("schema migration bypassed cached proof: calls=%d err=%v", calls, err)
	}
	db, err = openDB(filepath.Join(o.SQLiteHome, "state_5.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM unexpected").Scan(&value); err != nil || value != "preserve" {
		t.Fatalf("failed preflight changed source data: %q %v", value, err)
	}
}

func TestPreflightDoesNotCacheFailuresOrChangedExecutables(t *testing.T) {
	o, success := preflightFixture(t)
	var cache compatibilityCache
	if _, err := cache.check(t.Context(), o, func(context.Context, string, string) (*compatibilityProof, error) {
		return nil, &CompatibilityError{Reason: ProbeFailed}
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if len(cache.proofs) != 0 {
		t.Fatal("failure cached")
	}
	if _, err := cache.check(t.Context(), o, func(ctx context.Context, binary, version string) (*compatibilityProof, error) {
		proof, err := success(ctx, binary, version)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 2\n"), 0700); err != nil {
			return nil, err
		}
		return proof, nil
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("binary changed during proof was accepted")
	}
	if len(cache.proofs) != 0 {
		t.Fatal("proof for replaced executable cached")
	}
	if _, err := cache.check(t.Context(), o, success); err != nil {
		t.Fatalf("failed check prevented a later retry: %v", err)
	}
}

func TestPreflightCoalescesConcurrentChecksAndCancelsWaiters(t *testing.T) {
	o, success := preflightFixture(t)
	var cache compatibilityCache
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func(ctx context.Context, binary, version string) (*compatibilityProof, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return success(ctx, binary, version)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	leader := make(chan error, 1)
	go func() { _, err := cache.check(t.Context(), o, probe); leader <- err }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := cache.check(ctx, o, probe); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting check ignored cancellation: %v", err)
	}
	var group sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := cache.check(t.Context(), o, probe); results <- err }()
	}
	close(release)
	group.Wait()
	close(results)
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent checks ran %d probes", calls.Load())
	}
}

func TestPreflightRejectsNewStoresAndUnknownHistoryModes(t *testing.T) {
	o, _ := preflightFixture(t)
	if err := os.WriteFile(filepath.Join(o.SQLiteHome, "state_6.sqlite"), []byte("new store"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectNativeSchemas(t.Context(), o.SQLiteHome); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unrecognized native store accepted: %v", err)
	}
	export, _ := fixture(t, "unknown-future-mode")
	if _, err := Export(t.Context(), export); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unrecognized conversation layout accepted: %v", err)
	}
	if _, err := os.Stat(export.ProjectPath); err != nil {
		t.Fatal("unsupported conversation removed the project")
	}
}

func TestPreflightErrorsDoNotExposeNativeDiagnostics(t *testing.T) {
	for _, reason := range []string{SchemaChanged, HistoryChanged, RuntimeChanged, ProbeFailed} {
		err := &CompatibilityError{Reason: reason, cause: fmt.Errorf("SECRET account /private/path")}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "/private") || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsafe public error: %v", err)
		}
	}
}

func TestPreflightRejectsExecutableReportingAnotherVersion(t *testing.T) {
	binary, logPath := verificationFixtureBinary(t, Manifest{CodexVersion: "0.163.0"}, verificationFault{}, false)
	_, err := Preflight(t.Context(), PreflightOptions{CodexBinary: binary, CodexVersion: "0.169.0", SQLiteHome: t.TempDir()})
	var failure *CompatibilityError
	if !errors.As(err, &failure) || failure.Reason != RuntimeChanged {
		t.Fatalf("running/installed version mismatch accepted: %v", err)
	}
	events := readVerificationLog(t, logPath)
	if len(events) != 1 || events[0].Type != "version" {
		t.Fatalf("version mismatch reached native session operations: %#v", events)
	}
}

func TestVerifyRejectsSameVersionBinaryWithDifferentContents(t *testing.T) {
	o, _ := fixture(t, "legacy")
	m, err := Export(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	binary, logPath := verificationFixtureBinary(t, m, verificationFault{}, true)
	before, err := Digest(t.Context(), m.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	proof := &compatibilityProof{version: o.CodexVersion, binaryDigest: "different executable", schemas: nativeSchemaFingerprints[o.CodexVersion]}
	ctx := context.WithValue(t.Context(), compatibilityContextKey{}, proof)
	verified, err := Verify(ctx, m.ArchivePath, VerifyOptions{CodexBinary: binary})
	var failure *CompatibilityError
	if !errors.As(err, &failure) || failure.Reason != RuntimeChanged || verified.VerifiedSelfContained {
		t.Fatalf("same version bypassed binary fingerprint: %#v %v", verified, err)
	}
	after, err := Digest(t.Context(), m.ArchivePath)
	if err != nil || after != before {
		t.Fatal("rejected verification modified archive")
	}
	events := readVerificationLog(t, logPath)
	if len(events) != 1 || events[0].Type != "version" {
		t.Fatalf("changed executable reached native restore: %#v", events)
	}
}
