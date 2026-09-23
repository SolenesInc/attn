package ptybackend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptyhost"
)

func sharedArtifactTestRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "attn-artifacts-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestLastKnownGoodFromAnotherEnvironmentIsNotTrusted(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	previous := filepath.Join(root, "previous-host")
	writeScript(t, previous, "exit 0")
	id, err := ptyhost.HashArtifact(previous)
	if err != nil {
		t.Fatal(err)
	}
	dir := ptyhost.ArtifactsDir(root, "d-env")
	if _, err := ptyhost.ImportArtifact(dir, previous, id); err != nil {
		t.Fatal(err)
	}
	stale := sharedArtifactEnvironment()
	stale.ProbeContract++
	if err := ptyhost.WriteArtifactReceipt(dir, id, ptyhost.ArtifactReceipt{Environment: stale, Passed: true}); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "candidate-host")
	writeScript(t, candidate, "exit 1")
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-env", BinaryPath: candidate})
	if err != nil {
		t.Fatal(err)
	}
	if backend.SharedArtifactReady() {
		t.Fatal("a last-known-good build checked in another environment was trusted")
	}
}

func TestInterruptedCheckIsNotRecordedAsRejection(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	started := filepath.Join(root, "started")
	if err := syscall.Mkfifo(started, 0o600); err != nil {
		t.Fatal(err)
	}
	slow := filepath.Join(root, "slow-host")
	writeScript(t, slow, "echo started > '"+started+"'\nexec sleep 30")
	var rejections int
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-slow", BinaryPath: slow,
		OnSharedArtifactRejected: func(SharedArtifactRejection) error { rejections++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := make(chan error, 1)
	go func() { checked <- backend.ValidateSharedCandidate(ctx, false) }()
	if _, err := os.ReadFile(started); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-checked; err == nil {
		t.Fatal("a check that never finished passed")
	}
	if rejections != 0 || !backend.SharedCandidatePending() {
		t.Fatalf("an interrupted check was recorded: rejections=%d pending=%v", rejections, backend.SharedCandidatePending())
	}
	if _, err := backend.launchArtifact(); err == nil || !strings.Contains(err.Error(), "has not passed its check") {
		t.Fatalf("launch after an interrupted check = %v, want the unchecked build refused", err)
	}
}

func TestRejectedCandidateIsNotLaunchedWithoutAFallback(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	broken := filepath.Join(root, "broken-host")
	writeScript(t, broken, "exit 1")
	var rejections []SharedArtifactRejection
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-broken", BinaryPath: broken,
		OnSharedArtifactRejected: func(r SharedArtifactRejection) error { rejections = append(rejections, r); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.ValidateSharedCandidate(context.Background(), false); err == nil {
		t.Fatal("a host that exits at startup passed its check")
	}
	if len(rejections) != 1 || rejections[0].FallbackID != "" {
		t.Fatalf("rejections = %+v, want one without a fallback", rejections)
	}
	err = backend.Spawn(context.Background(), SpawnOptions{
		ID: "after-rejection", CWD: root, Agent: "lifecycle-probe", ExternalCommand: []string{"/bin/cat"},
	})
	if err == nil || !strings.Contains(err.Error(), "was rejected") {
		t.Fatalf("spawn with only a rejected build = %v, want a refusal", err)
	}
}

func TestUndeliveredRejectionIsReportedAgainWithoutRecheck(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	runs := filepath.Join(root, "runs")
	broken := filepath.Join(root, "broken-host")
	writeScript(t, broken, "echo run >> '"+runs+"'\nexit 1")
	cfg := WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-report", BinaryPath: broken,
		OnSharedArtifactRejected: func(SharedArtifactRejection) error { return errors.New("notification store unavailable") },
	}
	backend, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.ValidateSharedCandidate(context.Background(), false); err == nil {
		t.Fatal("a host that exits at startup passed its check")
	}
	if backend.SharedCandidatePending() {
		t.Fatal("a rejection whose report failed was not recorded")
	}

	var reports []SharedArtifactRejection
	cfg.OnSharedArtifactRejected = func(r SharedArtifactRejection) error { reports = append(reports, r); return nil }
	restarted, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ValidateSharedCandidate(context.Background(), false); err == nil {
		t.Fatal("a rejected build passed after restart")
	}
	data, err := os.ReadFile(runs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "run"); got != 1 || len(reports) != 1 || reports[0].Reason == "" {
		t.Fatalf("after restart: runs=%d reports=%+v, want the recorded rejection reported without a recheck", got, reports)
	}
}

func TestLastKnownGoodIsTheMostRecentlyPassedBuild(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	dir := ptyhost.ArtifactsDir(root, "d-latest")
	checked := time.Now().UTC()
	var ids []string
	for i, name := range []string{"older-host", "newer-host"} {
		path := filepath.Join(root, name)
		writeScript(t, path, "exit "+strconv.Itoa(i))
		id, err := ptyhost.HashArtifact(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ptyhost.ImportArtifact(dir, path, id); err != nil {
			t.Fatal(err)
		}
		receipt := ptyhost.ArtifactReceipt{Environment: sharedArtifactEnvironment(), Passed: true, CheckedAt: checked.Add(time.Duration(i) * time.Minute)}
		if err := ptyhost.WriteArtifactReceipt(dir, id, receipt); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	candidate := filepath.Join(root, "candidate-host")
	writeScript(t, candidate, "exit 9")
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-latest", BinaryPath: candidate})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := backend.launchArtifact()
	if err != nil || artifact.ID != ids[1] {
		t.Fatalf("launch artifact = %s, %v; want the most recently passed build %s", artifact.ID, err, ids[1])
	}
}
