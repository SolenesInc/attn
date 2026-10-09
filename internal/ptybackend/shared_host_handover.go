package ptybackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

var errHandoverUnsupported = errors.New("shared PTY host predates in-place handover")

type sharedHandover struct {
	done chan struct{}
	err  error
}

func (b *WorkerBackend) upgradeSharedTerminal(ctx context.Context, sessionID string) error {
	session, err := b.getSession(sessionID)
	if err != nil {
		return err
	}
	if err := b.handOverSharedHost(ctx, incarnationOf(session)); err != nil {
		return err
	}
	conn, _, _, err := b.connectAuthed(ctx, session)
	if err != nil {
		return fmt.Errorf("re-handshake after handover: %w", err)
	}
	_ = conn.Close()
	return nil
}

func (b *WorkerBackend) handOverSharedHost(ctx context.Context, inc hostIncarnation) error {
	b.handoverMu.Lock()
	if running := b.handovers[inc]; running != nil {
		b.handoverMu.Unlock()
		select {
		case <-running.done:
			return running.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &sharedHandover{done: make(chan struct{})}
	b.handovers[inc] = call
	b.handoverMu.Unlock()

	call.err = b.runSharedHandover(ctx, inc)
	b.handoverMu.Lock()
	delete(b.handovers, inc)
	b.handoverMu.Unlock()
	close(call.done)
	return call.err
}

func (b *WorkerBackend) runSharedHandover(ctx context.Context, inc hostIncarnation) error {
	before, err := b.sharedHostInfo(ctx, inc)
	if err != nil {
		return err
	}
	if before.SnapshotFormat == buildinfo.SnapshotFormat {
		return nil
	}
	if !slices.Contains(before.Capabilities, ptyhost.CapabilityHandover) {
		return fmt.Errorf("%w: it serves terminal format %s, this daemon reads %s", errHandoverUnsupported, before.SnapshotFormat, buildinfo.SnapshotFormat)
	}
	if err := b.ValidateSharedCandidate(ctx, false); err != nil {
		return err
	}
	target, err := b.launchArtifact()
	if err != nil {
		return err
	}
	entry, _, err := b.hostRegistryOf(inc)
	if err != nil {
		return err
	}
	if entry.ArtifactID == target.ID {
		return fmt.Errorf("shared PTY host already runs %s, the newest build that passed its check, and that build serves terminal format %s, not this daemon's %s", target.ID, before.SnapshotFormat, buildinfo.SnapshotFormat)
	}
	current, ok := ptyhost.StoredArtifact(b.artifactsDir, entry.ArtifactID)
	if !ok {
		return fmt.Errorf("the build the shared PTY host runs (%s) is no longer stored, so its handover to %s cannot be rehearsed", entry.ArtifactID, target.ID)
	}
	rehearsed := time.Now()
	if err := b.rehearseSharedHandover(ctx, current, target); err != nil {
		return fmt.Errorf("rehearse handover %s -> %s: %w", current.ID, target.ID, err)
	}
	started := time.Now()
	handedOver, err := b.requestHandover(ctx, inc, target)
	b.closeSharedControl(inc)
	if err != nil {
		return err
	}
	after, err := b.sharedHostInfo(ctx, inc)
	if err != nil {
		return fmt.Errorf("shared PTY host at %s did not answer after handing over to %s: %w", inc.socketPath, target.ID, err)
	}
	if after.HostPID != before.HostPID || after.SnapshotFormat != buildinfo.SnapshotFormat {
		return fmt.Errorf("handover to %s did not take effect: host pid %d -> %d, terminal format %s -> %s", target.ID, before.HostPID, after.HostPID, before.SnapshotFormat, after.SnapshotFormat)
	}
	b.cfg.Logf("shared PTY host handed over: socket=%s pid=%d terminals=%d %s -> %s rehearsal=%s handover=%s",
		inc.socketPath, after.HostPID, handedOver.Terminals, current.ID, target.ID,
		started.Sub(rehearsed).Round(time.Millisecond), time.Since(started).Round(time.Millisecond))
	return nil
}

func (b *WorkerBackend) requestHandover(ctx context.Context, inc hostIncarnation, target ptyhost.Artifact) (ptyhost.HandoverResult, error) {
	var result ptyhost.HandoverResult
	err := b.callSharedHost(ctx, inc, ptyhost.MethodHandover, ptyhost.HandoverParams{
		Executable: target.Path,
		Generation: target.ID,
	}, &result)
	if err != nil {
		return result, fmt.Errorf("hand over shared PTY host at %s to %s: %w", inc.socketPath, target.ID, err)
	}
	return result, nil
}

func (b *WorkerBackend) hostRegistryOf(inc hostIncarnation) (ptyhost.HostRegistry, string, error) {
	paths, _ := filepath.Glob(filepath.Join(ptyhost.HostRegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID), "*.json"))
	for _, path := range paths {
		entry, err := ptyhost.ReadHostRegistry(path)
		if err == nil && incarnationOfHost(entry) == inc {
			return entry, path, nil
		}
	}
	return ptyhost.HostRegistry{}, "", fmt.Errorf("no host registry entry names the shared PTY host at %s", inc.socketPath)
}

func (b *WorkerBackend) rehearseSharedHandover(ctx context.Context, from, to ptyhost.Artifact) error {
	b.hostMu.Lock()
	defer b.hostMu.Unlock()
	host, err := b.startSharedHost(ctx, from)
	if err != nil {
		return err
	}
	inc := incarnationOfHost(host)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRPCTimeout)
		defer cancel()
		if err := b.callSharedHost(stopCtx, inc, ptyhost.MethodShutdown, map[string]any{}, nil); err != nil {
			b.cfg.Logf("stop the shared PTY host that rehearsed a handover at %s: %v", inc.socketPath, err)
		}
		b.releaseSharedIncarnationIfUnused(inc)
		if !pidAlive(host.HostPID) {
			b.forgetDeadRehearsalHost(host)
		}
	}()

	probe, err := b.spawnCommittedProbe(ctx, host, from)
	if err != nil {
		return err
	}
	before, err := b.askProbe(ctx, probe, 80, 24)
	if err != nil {
		return fmt.Errorf("before the handover: %w", err)
	}
	if _, err := b.requestHandover(ctx, inc, to); err != nil {
		return err
	}
	b.closeSharedControl(inc)
	info, err := b.sharedHostInfo(ctx, inc)
	if err != nil {
		return fmt.Errorf("rehearsal host did not answer after the handover: %w", err)
	}
	if info.HostPID != host.HostPID || info.SnapshotFormat != buildinfo.SnapshotFormat {
		return fmt.Errorf("rehearsal host came back as pid %d with terminal format %s, want pid %d with %s", info.HostPID, info.SnapshotFormat, host.HostPID, buildinfo.SnapshotFormat)
	}
	screen, err := b.callScreenSnapshot(ctx, probe)
	if err != nil {
		return fmt.Errorf("read the probe screen after the handover: %w", err)
	}
	if screen.ScreenText == nil || !strings.Contains(*screen.ScreenText, before) {
		return fmt.Errorf("the probe screen lost %q across the handover", before)
	}
	if _, err := b.askProbe(ctx, probe, sharedHostProbeCols, sharedHostProbeRows); err != nil {
		return fmt.Errorf("after the handover: %w", err)
	}
	return nil
}

func (b *WorkerBackend) spawnCommittedProbe(ctx context.Context, host ptyhost.HostRegistry, artifact ptyhost.Artifact) (*workerSession, error) {
	probe, owner, err := b.spawnProbe(ctx, host, artifact)
	if err != nil {
		return nil, err
	}
	defer owner.Close()
	if err := b.commitSharedSession(ctx, incarnationOfHost(host), probe.SessionID); err != nil {
		return nil, fmt.Errorf("commit probe: %w", err)
	}
	return probe, nil
}

func (b *WorkerBackend) askProbe(ctx context.Context, probe *workerSession, cols, rows uint16) (string, error) {
	nonce, err := randomToken(8)
	if err != nil {
		return "", err
	}
	exited, watch, err := b.watchProbeExit(ctx, probe)
	if err != nil {
		return "", fmt.Errorf("watch probe: %w", err)
	}
	defer watch.Close()
	_, stream, err := b.attachSession(ctx, probe, "handover-rehearsal", AttachOptions{OmitReplay: true})
	if err != nil {
		return "", fmt.Errorf("attach probe: %w", err)
	}
	defer stream.Close()
	if _, err := b.resizeSession(ctx, probe, cols, rows, 0, 0); err != nil {
		return "", fmt.Errorf("resize probe: %w", err)
	}
	if err := b.inputSession(ctx, probe, []byte(nonce+"\r")); err != nil {
		return "", fmt.Errorf("send probe input: %w", err)
	}
	answer := fmt.Sprintf("ATTN-PROBE %s %dx%d", nonce, cols, rows)
	var output bytes.Buffer
	if err := awaitProbeOutput(ctx, stream, exited, &output, answer); err != nil {
		return "", err
	}
	return answer, nil
}

func (b *WorkerBackend) forgetDeadRehearsalHost(host ptyhost.HostRegistry) {
	if entry, path, err := b.hostRegistryOf(incarnationOfHost(host)); err == nil && entry.HostPID == host.HostPID {
		_ = os.Remove(path)
	}
	_ = os.Remove(host.SocketPath)
	paths, _ := filepath.Glob(filepath.Join(ptyhost.RegistryDir(b.cfg.DataRoot, b.cfg.DaemonInstanceID), sharedHostProbePrefix+"*.json"))
	for _, path := range paths {
		if entry, err := ptyworker.ReadRegistry(path); err == nil && entry.WorkerPID == host.HostPID {
			_ = os.Remove(path)
		}
	}
}
