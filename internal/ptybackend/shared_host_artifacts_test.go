package ptybackend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if err := ptyhost.SetLastKnownGood(dir, id); err != nil {
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
	slow := filepath.Join(root, "slow-host")
	writeScript(t, slow, "exec sleep 30")
	var rejections int
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-slow", BinaryPath: slow,
		OnSharedArtifactRejected: func(SharedArtifactRejection) { rejections++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := backend.ValidateSharedCandidate(ctx, false); err == nil {
		t.Fatal("a check that never finished passed")
	}
	if rejections != 0 || !backend.SharedCandidatePending() {
		t.Fatalf("an interrupted check was recorded: rejections=%d pending=%v", rejections, backend.SharedCandidatePending())
	}
}

func TestRejectedCandidateIsNotLaunchedWithoutAFallback(t *testing.T) {
	root := sharedArtifactTestRoot(t)
	broken := filepath.Join(root, "broken-host")
	writeScript(t, broken, "exit 1")
	var rejections []SharedArtifactRejection
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-broken", BinaryPath: broken,
		OnSharedArtifactRejected: func(r SharedArtifactRejection) { rejections = append(rejections, r) },
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
