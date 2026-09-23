package ptybackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
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
	sharedHostProbePrefix   = "probe-"
)

var errArtifactRejected = errors.New("shared PTY host build is broken")

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
		if artifact, ok := b.passedArtifact(b.candidate.id); ok {
			b.pinned, b.pinnedValidated = artifact, true
			return
		}
	}
	if artifact, ok := b.passedArtifact(ptyhost.LastKnownGood(b.artifactsDir)); ok {
		b.pinned, b.pinnedValidated = artifact, true
	}
}

func (b *WorkerBackend) passedArtifact(id string) (ptyhost.Artifact, bool) {
	if id == "" {
		return ptyhost.Artifact{}, false
	}
	receipt, err := ptyhost.ReadArtifactReceipt(b.artifactsDir, id)
	if err != nil || !receipt.Passed || receipt.Environment != sharedArtifactEnvironment() {
		return ptyhost.Artifact{}, false
	}
	return ptyhost.StoredArtifact(b.artifactsDir, id)
}

func (b *WorkerBackend) candidateRejection() (string, bool) {
	receipt, err := ptyhost.ReadArtifactReceipt(b.artifactsDir, b.candidate.id)
	if err != nil || receipt.Passed || receipt.Environment != sharedArtifactEnvironment() {
		return "", false
	}
	return receipt.Reason, true
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
	if reason, rejected := b.candidateRejection(); rejected {
		return ptyhost.Artifact{}, fmt.Errorf("shared PTY host %s was rejected: %s", b.candidate.id, reason)
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
	if reason, rejected := b.candidateRejection(); rejected && !explicit {
		return fmt.Errorf("shared PTY host %s was rejected: %s", b.candidate.id, reason)
	}
	artifact, err := b.importCandidate()
	if err != nil {
		return err
	}
	if passed, ok := b.passedArtifact(artifact.ID); ok {
		return b.promoteSharedArtifact(passed)
	}

	started := time.Now()
	probeErr := b.probeSharedArtifact(ctx, artifact)
	if probeErr != nil && !errors.Is(probeErr, errArtifactRejected) {
		return fmt.Errorf("shared PTY host %s could not be checked: %w", artifact.ID, probeErr)
	}
	if probeErr != nil {
		return b.rejectSharedArtifact(artifact, probeErr)
	}
	if err := b.recordSharedArtifact(artifact, nil); err != nil {
		return err
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

func (b *WorkerBackend) recordSharedArtifact(artifact ptyhost.Artifact, probeErr error) error {
	receipt := ptyhost.ArtifactReceipt{Environment: sharedArtifactEnvironment(), Passed: probeErr == nil}
	if probeErr != nil {
		receipt.Reason = probeErr.Error()
	}
	if err := ptyhost.WriteArtifactReceipt(b.artifactsDir, artifact.ID, receipt); err != nil {
		return fmt.Errorf("record shared PTY host validation: %w", err)
	}
	return nil
}

func (b *WorkerBackend) rejectSharedArtifact(artifact ptyhost.Artifact, probeErr error) error {
	b.artifactMu.Lock()
	fallback := ""
	if b.pinnedValidated && b.pinned.ID != artifact.ID {
		fallback = b.pinned.ID
	}
	b.artifactMu.Unlock()
	if b.cfg.OnSharedArtifactRejected != nil {
		err := b.cfg.OnSharedArtifactRejected(SharedArtifactRejection{
			ArtifactID: artifact.ID,
			Source:     b.candidate.source,
			Reason:     probeErr.Error(),
			FallbackID: fallback,
		})
		if err != nil {
			return fmt.Errorf("report rejected shared PTY host %s: %w", artifact.ID, err)
		}
	}
	if err := b.recordSharedArtifact(artifact, probeErr); err != nil {
		return err
	}
	b.artifactMu.Lock()
	if b.pinned.ID == artifact.ID {
		b.pinned, b.pinnedValidated = ptyhost.Artifact{}, false
		if err := ptyhost.SetLastKnownGood(b.artifactsDir, ""); err != nil {
			b.cfg.Logf("clear rejected shared PTY host %s: %v", artifact.ID, err)
		}
	}
	b.artifactMu.Unlock()
	b.cfg.Logf("shared PTY host artifact %s rejected: %s (fallback %q)", artifact.ID, probeErr, fallback)
	return fmt.Errorf("shared PTY host %s failed validation: %w", artifact.ID, probeErr)
}

func (b *WorkerBackend) collectSharedArtifacts() {
	b.hostMu.Lock()
	defer b.hostMu.Unlock()
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

func (b *WorkerBackend) probeSharedArtifact(ctx context.Context, artifact ptyhost.Artifact) error {
	nonce, err := randomToken(8)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		host, err := b.ensureSharedHost(ctx, &artifact)
		if err != nil {
			return err
		}
		err = b.roundTripProbe(ctx, artifact, host, nonce)
		switch {
		case err == nil, errors.Is(err, errArtifactRejected):
			return err
		case ctx.Err() != nil:
			return fmt.Errorf("%v: %w", err, ctx.Err())
		case attempt == 0 && isRetiringSharedHost(err):
			continue
		default:
			return fmt.Errorf("%w: %w", errArtifactRejected, err)
		}
	}
}

func (b *WorkerBackend) roundTripProbe(ctx context.Context, artifact ptyhost.Artifact, host ptyhost.HostRegistry, nonce string) error {
	inc := incarnationOfHost(host)
	info, err := b.sharedHostInfo(ctx, inc)
	if err != nil {
		return err
	}
	if !slices.Contains(info.Capabilities, ptyhost.CapabilityProbeChild) {
		b.stopUnusedRejectedHost(artifact, inc, info)
		return fmt.Errorf("%w: host does not provide the validation probe", errArtifactRejected)
	}

	suffix, err := randomToken(6)
	if err != nil {
		return err
	}
	probe := &workerSession{
		SessionID:    sharedHostProbePrefix + suffix,
		SocketPath:   host.SocketPath,
		ControlToken: host.ControlToken,
		WorkerPID:    host.HostPID,
	}
	workdir := os.TempDir()
	owner, err := b.openSharedCall(ctx, inc.endpoint(), ptyhost.MethodSpawn, ptyhost.SpawnParams{
		SessionID: probe.SessionID,
		Agent:     "probe",
		CWD:       workdir,
		Cols:      80,
		Rows:      24,
		Ephemeral: true,
		Attempts: []pty.PreparedLaunchAttempt{{
			Executable: artifact.Path,
			Args:       []string{artifact.Path, ptyhost.ProbeChildFlag},
			Env:        []string{"TERM=xterm-256color"},
			CWD:        workdir,
		}},
	}, nil)
	if err != nil {
		return fmt.Errorf("spawn probe: %w", err)
	}
	defer owner.Close()
	defer b.releaseSharedIncarnationIfUnused(inc)
	exited, watch, err := b.watchProbeExit(ctx, probe)
	if err != nil {
		return fmt.Errorf("watch probe: %w", err)
	}
	defer watch.Close()

	attached, stream, err := b.attachSession(ctx, probe, "artifact-probe")
	if err != nil {
		return fmt.Errorf("attach probe: %w", err)
	}
	defer stream.Close()
	if !attached.Running {
		return errors.New("probe child exited before answering")
	}
	if _, err := b.resizeSession(ctx, probe, sharedHostProbeCols, sharedHostProbeRows, 0, 0); err != nil {
		return fmt.Errorf("resize probe: %w", err)
	}
	if err := b.inputSession(ctx, probe, []byte(nonce+"\r")); err != nil {
		return fmt.Errorf("send probe input: %w", err)
	}
	answer := fmt.Sprintf("ATTN-PROBE %s %dx%d", nonce, sharedHostProbeCols, sharedHostProbeRows)
	var output bytes.Buffer
	if err := awaitProbeOutput(ctx, stream, exited, &output, answer); err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return fmt.Errorf("detach probe: %w", err)
	}
	return nil
}

func (b *WorkerBackend) watchProbeExit(ctx context.Context, probe *workerSession) (<-chan struct{}, net.Conn, error) {
	rpcCtx, cancel := withDefaultRPCTimeout(ctx)
	defer cancel()
	conn, enc, dec, err := b.connectAuthed(rpcCtx, probe)
	if err != nil {
		return nil, nil, err
	}
	if err := b.completeSharedCall(rpcCtx, conn, enc, dec, probe, ptyworker.MethodWatch, map[string]any{}, nil); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for {
			frameType, _, evt, err := readFrame(dec)
			if err != nil || frameType == "evt" && evt.Event == ptyworker.EventExit {
				return
			}
		}
	}()
	return exited, conn, nil
}

func awaitProbeOutput(ctx context.Context, stream Stream, exited <-chan struct{}, output *bytes.Buffer, want string) error {
	for !bytes.Contains(output.Bytes(), []byte(want)) {
		select {
		case event, ok := <-stream.Events():
			if !ok {
				return fmt.Errorf("probe output ended before %q (output %q)", want, tail(output.Bytes()))
			}
			if event.Kind == OutputEventKindOutput {
				output.Write(event.Data)
			}
		case <-exited:
			return fmt.Errorf("probe child exited before %q (output %q)", want, tail(output.Bytes()))
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

func (b *WorkerBackend) stopUnusedRejectedHost(artifact ptyhost.Artifact, inc hostIncarnation, info ptyhost.HostInfoResult) {
	b.artifactMu.Lock()
	pinned := b.pinned.ID == artifact.ID
	b.artifactMu.Unlock()
	if pinned || len(info.SessionIDs) > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	if err := b.callSharedHost(ctx, inc, ptyhost.MethodShutdown, map[string]any{}, nil); err != nil {
		b.cfg.Logf("stop rejected shared PTY host at %s: %v", inc.socketPath, err)
	}
}
