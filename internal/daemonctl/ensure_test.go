package daemonctl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDaemonProcessWaitForReadyReturnsOnStartupSignal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	blockReader, blockWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "printf 'ready\\n' >&3; cat <&4 >/dev/null")
	cmd.ExtraFiles = []*os.File{writer, blockReader}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	blockReader.Close()
	defer func() {
		blockWriter.Close()
		_ = cmd.Wait()
	}()

	if err := (daemonProcess{ready: reader}).waitForReady(context.Background()); err != nil {
		t.Fatalf("waitForReady() error = %v", err)
	}
}

func TestDaemonProcessWaitForReadyReportsStartupFailure(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = writer.WriteString("error:open database\n")
		_ = writer.Close()
	}()

	err = (daemonProcess{ready: reader}).waitForReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "open database") {
		t.Fatalf("waitForReady() error = %v, want startup failure", err)
	}
}

func TestDaemonProcessWaitForReadyReportsNonDaemonLockHolder(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = writer.WriteString("lock-held\n")
		_ = writer.Close()
	}()

	err = (daemonProcess{ready: reader}).waitForReady(context.Background())
	if !errors.Is(err, errDaemonLockHeld) {
		t.Fatalf("waitForReady() error = %v, want non-daemon lock holder", err)
	}
}

func TestWaitForMatchingDaemonReconcilesAConcurrentStartup(t *testing.T) {
	retry := make(chan time.Time)
	firstAttempt := make(chan struct{})
	fetches := 0
	fetch := func(context.Context) (healthResponse, error) {
		fetches++
		if fetches == 1 {
			close(firstAttempt)
			return healthResponse{}, errors.New("not ready")
		}
		return healthResponse{Protocol: protocol.ProtocolVersion}, nil
	}
	previousFingerprint := buildinfo.SourceFingerprint
	buildinfo.SourceFingerprint = "unknown"
	t.Cleanup(func() { buildinfo.SourceFingerprint = previousFingerprint })
	go func() {
		<-firstAttempt
		retry <- time.Time{}
	}()

	if err := waitForMatchingDaemon(context.Background(), retry, fetch, func() bool { return true }); err != nil {
		t.Fatalf("waitForMatchingDaemon() error = %v", err)
	}
}

func TestWaitForMatchingDaemonRejectsAHealthyDaemonWithoutItsUnixSocket(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	buildinfo.SourceFingerprint = "unknown"
	t.Cleanup(func() { buildinfo.SourceFingerprint = previousFingerprint })

	err := waitForMatchingDaemon(
		context.Background(),
		make(chan time.Time),
		func(context.Context) (healthResponse, error) {
			return healthResponse{Protocol: protocol.ProtocolVersion}, nil
		},
		func() bool { return false },
	)
	if err == nil || !strings.Contains(err.Error(), "missing its Unix listener") {
		t.Fatalf("waitForMatchingDaemon() error = %v, want missing Unix listener rejection", err)
	}
}

func TestEnsureTripwireNamesLimitWhenChildNeverSignals(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errDaemonStartupTimeout)
	run := func(ctx context.Context, _ string) (EnsureResult, error) {
		_, err := waitForSpawnedDaemon(ctx, daemonProcess{ready: reader})
		return EnsureResult{}, err
	}

	_, err = ensureWithTripwire(ctx, "/tmp/attn", run, func(context.Context) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "daemon startup wait exceeded 1m0s") || !strings.Contains(err.Error(), "daemon.log") {
		t.Fatalf("ensureWithTripwire() error = %v, want named limit and diagnostic path", err)
	}
}

func TestEnsureTripwireAcceptsDaemonThatCrossesTheBoundary(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errDaemonStartupTimeout)
	run := func(ctx context.Context, _ string) (EnsureResult, error) {
		return EnsureResult{}, ctx.Err()
	}

	result, err := ensureWithTripwire(ctx, "/tmp/attn", run, func(context.Context) bool { return true })
	if err != nil {
		t.Fatalf("ensureWithTripwire() error = %v", err)
	}
	if result.Status != "already_running" {
		t.Fatalf("ensureWithTripwire() status = %q, want already_running", result.Status)
	}
}

func TestEnsureLockSerializesSocketInspection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_DATA_DIR", dir)
	t.Setenv("ATTN_SOCKET_PATH", "")
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	config.ReloadForTesting()

	release, err := acquireEnsureLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acquireEnsureLock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("second acquireEnsureLock() error = %v, want context cancellation while first holds lock", err)
	}
	release()

	release, err = acquireEnsureLock(context.Background())
	if err != nil {
		t.Fatalf("acquireEnsureLock() after release: %v", err)
	}
	release()
}

func TestWaitForPIDLockReleaseUsesTheLockAsItsSignal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_DATA_DIR", dir)
	t.Setenv("ATTN_SOCKET_PATH", "")
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	config.ReloadForTesting()

	holder, err := os.OpenFile(config.PIDPath(), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForPIDLockRelease(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForPIDLockRelease() error = %v, want context cancellation while lock is held", err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := waitForPIDLockRelease(context.Background()); err != nil {
		t.Fatalf("waitForPIDLockRelease() after release: %v", err)
	}
}

func TestDaemonProcessWaitForReadyStopsWithContext(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = (daemonProcess{ready: reader}).waitForReady(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForReady() error = %v, want context cancellation", err)
	}
}

func TestDaemonMatchesCurrentBinary_UsesSourceFingerprintWhenAvailable(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "tree:new"

	if !daemonMatchesCurrentBinary(healthResponse{SourceFingerprint: "tree:new", Protocol: "old"}) {
		t.Fatal("expected matching source fingerprint to win")
	}
	if daemonMatchesCurrentBinary(healthResponse{SourceFingerprint: "tree:old", Protocol: protocol.ProtocolVersion}) {
		t.Fatal("expected mismatched source fingerprint to fail")
	}
}

func TestDaemonMatchesCurrentBinary_FallsBackToProtocolWhenFingerprintUnknown(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "unknown"

	if !daemonMatchesCurrentBinary(healthResponse{Protocol: protocol.ProtocolVersion}) {
		t.Fatal("expected protocol fallback match")
	}
	if daemonMatchesCurrentBinary(healthResponse{Protocol: "999"}) {
		t.Fatal("expected protocol fallback mismatch")
	}
}

func TestMismatchReason_ReportsMissingFingerprint(t *testing.T) {
	previousFingerprint := buildinfo.SourceFingerprint
	t.Cleanup(func() {
		buildinfo.SourceFingerprint = previousFingerprint
	})
	buildinfo.SourceFingerprint = "tree:new"

	if got := mismatchReason(nil, healthResponse{}); got != "source_fingerprint_missing" {
		t.Fatalf("mismatchReason() = %q, want source_fingerprint_missing", got)
	}
}

// The PID file's exclusive flock is the sole mutual-exclusion mechanism: unlinking it here
// would leave a concurrent holder locking an orphaned inode at the same pathname.
func TestRemoveStaleSocketFiles_LeavesPIDFileInPlace(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "attn.sock")
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", socketPath)
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	config.ReloadForTesting()

	pidPath := config.PIDPath()
	if err := os.WriteFile(socketPath, []byte("stale socket"), 0644); err != nil {
		t.Fatalf("write stale socket file: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte("12345"), 0644); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	if err := removeStaleSocketFiles(); err != nil {
		t.Fatalf("removeStaleSocketFiles error: %v", err)
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale socket to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("expected pid file to remain on disk, stat err = %v", err)
	}
}

func TestEnsure_RejectsMixedSocketAndDefaultStoreIsolation(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "attn.sock"))
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	config.ReloadForTesting()

	_, err := Ensure(context.Background(), "/tmp/attn")
	if err == nil {
		t.Fatal("Ensure() accepted an alternate socket root with the default profile DB")
	}
	if !strings.Contains(err.Error(), "refusing to start daemon") {
		t.Fatalf("Ensure() error = %q, want isolation refusal", err)
	}
}
