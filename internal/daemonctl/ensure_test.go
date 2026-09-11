package daemonctl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	if err := waitForMatchingDaemon(context.Background(), retry, fetch); err != nil {
		t.Fatalf("waitForMatchingDaemon() error = %v", err)
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
