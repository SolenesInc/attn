package ptybackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

const (
	sharedHostProbeContract = 1
	sharedHostProbeCols     = 97
	sharedHostProbeRows     = 31
)

type SharedArtifactRejection struct {
	ArtifactID string
	Source     string
	Reason     string
	FallbackID string
}

type sharedCandidate struct {
	source string
	id     string
	err    error
}

func (b *WorkerBackend) loadSharedArtifacts() {
	b.artifactsDir = ptyhost.ArtifactsDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID)
	b.candidate.source = b.cfg.BinaryPath
	b.candidate.id, b.candidate.err = ptyhost.HashArtifact(b.cfg.BinaryPath)
	if b.candidate.err == nil {
		receipt, err := ptyhost.ReadArtifactReceipt(b.artifactsDir, b.candidate.id)
		if err == nil && receipt.Passed && receipt.Environment == sharedArtifactEnvironment() {
			if artifact, stored := ptyhost.StoredArtifact(b.artifactsDir, b.candidate.id); stored {
				b.pinned, b.pinnedValidated = artifact, true
				return
			}
		}
	}
	if id := ptyhost.LastKnownGood(b.artifactsDir); id != "" {
		if artifact, stored := ptyhost.StoredArtifact(b.artifactsDir, id); stored {
			b.pinned, b.pinnedValidated = artifact, true
		}
	}
}

func sharedArtifactEnvironment() ptyhost.ArtifactEnvironment {
	return ptyhost.ArtifactEnvironment{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		CoreProtocol:   ptyworker.RPCMajor,
		SnapshotFormat: buildinfo.SnapshotFormat,
		ProbeContract:  sharedHostProbeContract,
	}
}

func (b *WorkerBackend) SharedArtifactReady() bool {
	b.artifactMu.Lock()
	defer b.artifactMu.Unlock()
	return b.pinnedValidated
}

func (b *WorkerBackend) SharedCandidateError() error {
	return b.candidate.err
}

func (b *WorkerBackend) SharedCandidatePending() bool {
	if b.candidate.err != nil {
		return false
	}
	receipt, err := ptyhost.ReadArtifactReceipt(b.artifactsDir, b.candidate.id)
	if err != nil || receipt.Environment != sharedArtifactEnvironment() {
		return true
	}
	b.artifactMu.Lock()
	defer b.artifactMu.Unlock()
	return receipt.Passed && b.pinned.ID != b.candidate.id
}

func (b *WorkerBackend) launchArtifact() (ptyhost.Artifact, error) {
	b.artifactMu.Lock()
	pinned := b.pinned
	b.artifactMu.Unlock()
	if pinned.ID != "" {
		return pinned, nil
	}
	return b.importCandidate()
}

func (b *WorkerBackend) importCandidate() (ptyhost.Artifact, error) {
	if b.candidate.err != nil {
		return ptyhost.Artifact{}, b.candidate.err
	}
	artifact, err := ptyhost.ImportArtifact(b.artifactsDir, b.candidate.source, b.candidate.id)
	if err != nil {
		return ptyhost.Artifact{}, fmt.Errorf("pin shared PTY host %s: %w", b.candidate.source, err)
	}
	return artifact, nil
}

func (b *WorkerBackend) ValidateSharedCandidate(ctx context.Context, explicit bool) error {
	b.validateMu.Lock()
	defer b.validateMu.Unlock()
	if b.candidate.err != nil {
		return b.candidate.err
	}
	environment := sharedArtifactEnvironment()
	receipt, err := ptyhost.ReadArtifactReceipt(b.artifactsDir, b.candidate.id)
	recorded := err == nil && receipt.Environment == environment
	if recorded && !receipt.Passed && !explicit {
		return fmt.Errorf("shared PTY host %s was rejected: %s", b.candidate.id, receipt.Reason)
	}
	artifact, err := b.importCandidate()
	if err != nil {
		return err
	}
	if recorded && receipt.Passed {
		return b.promoteSharedArtifact(artifact)
	}

	started := time.Now()
	probeErr := b.probeSharedArtifact(ctx, artifact)
	if probeErr != nil && errors.Is(ctx.Err(), context.Canceled) {
		return probeErr
	}
	receipt = ptyhost.ArtifactReceipt{
		Environment: environment,
		Passed:      probeErr == nil,
		CheckedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if probeErr != nil {
		receipt.Reason = probeErr.Error()
	}
	if err := ptyhost.WriteArtifactReceipt(b.artifactsDir, artifact.ID, receipt); err != nil {
		return fmt.Errorf("record shared PTY host validation: %w", err)
	}
	if probeErr != nil {
		b.rejectSharedArtifact(artifact, probeErr.Error())
		return fmt.Errorf("shared PTY host %s failed validation: %w", artifact.ID, probeErr)
	}
	b.cfg.Logf("shared PTY host artifact %s passed validation in %s", artifact.ID, time.Since(started).Round(time.Millisecond))
	return b.promoteSharedArtifact(artifact)
}

func (b *WorkerBackend) promoteSharedArtifact(artifact ptyhost.Artifact) error {
	if err := ptyhost.SetLastKnownGood(b.artifactsDir, artifact.ID); err != nil {
		return fmt.Errorf("promote shared PTY host %s: %w", artifact.ID, err)
	}
	b.artifactMu.Lock()
	previous := b.pinned.ID
	b.pinned, b.pinnedValidated = artifact, true
	b.artifactMu.Unlock()
	if previous != artifact.ID {
		b.cfg.Logf("shared PTY host artifact promoted: %s (previous %q)", artifact.ID, previous)
	}
	b.collectSharedArtifacts()
	return nil
}

func (b *WorkerBackend) rejectSharedArtifact(artifact ptyhost.Artifact, reason string) {
	b.artifactMu.Lock()
	if b.pinned.ID == artifact.ID {
		b.pinned, b.pinnedValidated = ptyhost.Artifact{}, false
		if err := ptyhost.SetLastKnownGood(b.artifactsDir, ""); err != nil {
			b.cfg.Logf("clear rejected shared PTY host %s: %v", artifact.ID, err)
		}
	}
	fallback := ""
	if b.pinnedValidated {
		fallback = b.pinned.ID
	}
	b.artifactMu.Unlock()
	b.cfg.Logf("shared PTY host artifact %s rejected: %s (fallback %q)", artifact.ID, reason, fallback)
	if b.cfg.OnSharedArtifactRejected != nil {
		b.cfg.OnSharedArtifactRejected(SharedArtifactRejection{
			ArtifactID: artifact.ID,
			Source:     b.candidate.source,
			Reason:     reason,
			FallbackID: fallback,
		})
	}
}

func (b *WorkerBackend) collectSharedArtifacts() {
	keep := map[string]bool{b.candidate.id: true, ptyhost.LastKnownGood(b.artifactsDir): true}
	b.artifactMu.Lock()
	keep[b.pinned.ID] = true
	b.artifactMu.Unlock()
	for _, path := range ptyhost.HostRegistryPaths(b.cfg.DataRoot) {
		if entry, err := ptyhost.ReadHostRegistry(path); err == nil && pidAlive(entry.HostPID) {
			keep[entry.ArtifactID] = true
		}
	}
	for _, id := range ptyhost.StoredArtifactIDs(b.artifactsDir) {
		if keep[id] {
			continue
		}
		if err := ptyhost.RemoveArtifact(b.artifactsDir, id); err != nil {
			b.cfg.Logf("remove retired shared PTY host artifact %s: %v", id, err)
		}
	}
}

func (b *WorkerBackend) probeSharedArtifact(ctx context.Context, artifact ptyhost.Artifact) (err error) {
	host, err := b.ensureSharedHost(ctx, artifact)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			b.retireIdleSharedHost(incarnationOfHost(host))
		}
	}()
	info, err := b.sharedHostInfo(ctx, incarnationOfHost(host))
	if err != nil {
		return err
	}
	if !slices.Contains(info.Capabilities, ptyhost.CapabilityProbeChild) {
		return errors.New("host does not provide the validation probe")
	}

	suffix, err := randomToken(6)
	if err != nil {
		return err
	}
	id := "probe-" + suffix
	workdir := os.TempDir()
	params := ptyhost.SpawnParams{
		SessionID: id,
		Agent:     "probe",
		CWD:       workdir,
		Cols:      80,
		Rows:      24,
		Attempts: []pty.PreparedLaunchAttempt{{
			Executable: artifact.Path,
			Args:       []string{artifact.Path, ptyhost.ProbeChildFlag},
			Env:        []string{"TERM=xterm-256color"},
			CWD:        workdir,
		}},
	}
	if _, _, err := b.spawnOnSharedHost(ctx, artifact, params); err != nil {
		return fmt.Errorf("spawn probe: %w", err)
	}
	removed := false
	defer func() {
		if removed {
			return
		}
		removeCtx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
		defer cancel()
		_ = b.Remove(removeCtx, id)
	}()

	attached, stream, err := b.Attach(ctx, id, "artifact-probe")
	if err != nil {
		return fmt.Errorf("attach probe: %w", err)
	}
	defer stream.Close()
	if !attached.Running {
		return errors.New("probe child exited before answering")
	}
	if _, err := b.Resize(ctx, id, sharedHostProbeCols, sharedHostProbeRows, 0, 0); err != nil {
		return fmt.Errorf("resize probe: %w", err)
	}
	nonce, err := randomToken(8)
	if err != nil {
		return err
	}
	if err := b.Input(ctx, id, []byte(nonce+"\r")); err != nil {
		return fmt.Errorf("send probe input: %w", err)
	}
	answer := fmt.Sprintf("ATTN-PROBE %s %dx%d", nonce, sharedHostProbeCols, sharedHostProbeRows)
	var output bytes.Buffer
	if err := awaitProbeOutput(ctx, stream, &output, answer); err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return fmt.Errorf("detach probe: %w", err)
	}
	removed = true
	if err := b.Remove(ctx, id); err != nil {
		return fmt.Errorf("remove probe: %w", err)
	}
	return nil
}

func awaitProbeOutput(ctx context.Context, stream Stream, output *bytes.Buffer, want string) error {
	for !bytes.Contains(output.Bytes(), []byte(want)) {
		select {
		case event, ok := <-stream.Events():
			if !ok {
				return fmt.Errorf("probe output ended before %q (output %q)", want, tail(output.Bytes()))
			}
			if event.Kind == OutputEventKindOutput {
				output.Write(event.Data)
			}
		case <-ctx.Done():
			return fmt.Errorf("probe output never showed %q (output %q): %w", want, tail(output.Bytes()), ctx.Err())
		}
	}
	return nil
}

func tail(data []byte) []byte {
	const limit = 160
	if len(data) <= limit {
		return data
	}
	return data[len(data)-limit:]
}

func (b *WorkerBackend) retireIdleSharedHost(inc hostIncarnation) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	info, err := b.sharedHostInfo(ctx, inc)
	if err != nil || len(info.SessionIDs) > 0 {
		return
	}
	if err := b.callSharedHost(ctx, inc, ptyhost.MethodShutdown, map[string]any{}, nil); err != nil {
		b.cfg.Logf("stop rejected shared PTY host at %s: %v", inc.socketPath, err)
	}
}
