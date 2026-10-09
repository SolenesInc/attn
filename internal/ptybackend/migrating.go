package ptybackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

type runtimeOwner uint8

const (
	ownerLegacy runtimeOwner = iota + 1
	ownerShared
)

type MigratingBackend struct {
	legacy Backend
	shared Backend

	mu           sync.RWMutex
	owners       map[harness.TerminalID]runtimeOwner
	pendingSpawn map[harness.TerminalID]struct{}
	useShared    bool
}

func NewMigrating(legacy, shared Backend, useSharedForNewSessions bool) (*MigratingBackend, error) {
	if legacy == nil {
		return nil, errors.New("missing legacy PTY backend")
	}
	if shared == nil {
		return nil, errors.New("missing shared PTY backend")
	}
	return &MigratingBackend{
		legacy:       legacy,
		shared:       shared,
		owners:       make(map[harness.TerminalID]runtimeOwner),
		pendingSpawn: make(map[harness.TerminalID]struct{}),
		useShared:    useSharedForNewSessions,
	}, nil
}

func (b *MigratingBackend) PTYBackendMode() string {
	return "migrating"
}

func (b *MigratingBackend) ProbeShared(ctx context.Context) error {
	probe, ok := b.shared.(interface{ Probe(context.Context) error })
	if !ok {
		return errors.New("shared PTY backend does not support probing")
	}
	return probe.Probe(ctx)
}

func (b *MigratingBackend) SetSharedForNewSessions(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.useShared = enabled
}

func (b *MigratingBackend) SharedForNewSessions() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.useShared
}

func (b *MigratingBackend) SetExitHandler(handler func(ExitInfo)) {
	for _, backend := range []Backend{b.legacy, b.shared} {
		if hooks, ok := backend.(LifecycleHooks); ok {
			hooks.SetExitHandler(handler)
		}
	}
}

func (b *MigratingBackend) SetStateHandler(handler func(id harness.TerminalID, obs pty.Observation)) {
	for _, backend := range []Backend{b.legacy, b.shared} {
		if hooks, ok := backend.(LifecycleHooks); ok {
			hooks.SetStateHandler(handler)
		}
	}
}

func (b *MigratingBackend) Spawn(ctx context.Context, opts SpawnOptions) error {
	if opts.ID == "" {
		return errors.New("missing session id")
	}

	b.mu.Lock()
	if _, exists := b.owners[opts.ID]; exists {
		b.mu.Unlock()
		return fmt.Errorf("session %s already exists", opts.ID)
	}
	if _, pending := b.pendingSpawn[opts.ID]; pending {
		b.mu.Unlock()
		return fmt.Errorf("session %s spawn already in progress", opts.ID)
	}
	b.pendingSpawn[opts.ID] = struct{}{}
	owner := ownerLegacy
	backend := b.legacy
	if b.useShared {
		owner = ownerShared
		backend = b.shared
	}
	b.mu.Unlock()

	err := backend.Spawn(ctx, opts)
	b.mu.Lock()
	delete(b.pendingSpawn, opts.ID)
	if err == nil {
		b.owners[opts.ID] = owner
	}
	b.mu.Unlock()
	return err
}

func (b *MigratingBackend) Attach(ctx context.Context, id harness.TerminalID, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error) {
	backend, err := b.backendFor(id)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	return backend.Attach(ctx, id, subscriberID, opts...)
}

func (b *MigratingBackend) Input(ctx context.Context, id harness.TerminalID, data []byte) error {
	backend, err := b.backendFor(id)
	if err != nil {
		return err
	}
	return backend.Input(ctx, id, data)
}

func (b *MigratingBackend) Resize(ctx context.Context, id harness.TerminalID, cols, rows, xpixel, ypixel uint16) (ResizeResult, error) {
	backend, err := b.backendFor(id)
	if err != nil {
		return ResizeResult{}, err
	}
	return backend.Resize(ctx, id, cols, rows, xpixel, ypixel)
}

func (b *MigratingBackend) SetTheme(ctx context.Context, id harness.TerminalID, theme pty.TerminalTheme) error {
	backend, err := b.backendFor(id)
	if err != nil {
		return err
	}
	return backend.SetTheme(ctx, id, theme)
}

func (b *MigratingBackend) Kill(ctx context.Context, id harness.TerminalID, sig syscall.Signal) error {
	backend, err := b.backendFor(id)
	if err != nil {
		return err
	}
	return backend.Kill(ctx, id, sig)
}

func (b *MigratingBackend) Remove(ctx context.Context, id harness.TerminalID) error {
	backend, err := b.backendFor(id)
	if err != nil {
		return err
	}
	err = backend.Remove(ctx, id)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		b.mu.Lock()
		delete(b.owners, id)
		b.mu.Unlock()
	}
	return err
}

func (b *MigratingBackend) TerminalIDs(_ context.Context) []harness.TerminalID {
	b.mu.RLock()
	ids := make([]harness.TerminalID, 0, len(b.owners))
	for id := range b.owners {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	slices.Sort(ids)
	return ids
}

func (b *MigratingBackend) Recover(ctx context.Context) (RecoveryReport, error) {
	legacyReport, legacyErr := b.legacy.Recover(ctx)
	sharedReport, sharedErr := b.shared.Recover(ctx)
	report := addRecoveryReports(legacyReport, sharedReport)

	owners := make(map[harness.TerminalID]runtimeOwner)
	var conflicts []harness.TerminalID
	for _, recovered := range []struct {
		owner   runtimeOwner
		backend Backend
	}{
		{owner: ownerLegacy, backend: b.legacy},
		{owner: ownerShared, backend: b.shared},
	} {
		for _, id := range recovered.backend.TerminalIDs(ctx) {
			if _, exists := owners[id]; exists {
				delete(owners, id)
				conflicts = append(conflicts, id)
				continue
			}
			owners[id] = recovered.owner
		}
	}

	b.mu.Lock()
	b.owners = owners
	b.mu.Unlock()

	var conflictErr error
	if len(conflicts) > 0 {
		slices.Sort(conflicts)
		report.Failed += len(conflicts)
		conflictErr = fmt.Errorf("PTY sessions claimed by both runtimes: %v", conflicts)
	}
	return report, errors.Join(legacyErr, sharedErr, conflictErr)
}

func addRecoveryReports(a, b RecoveryReport) RecoveryReport {
	return RecoveryReport{
		Recovered: a.Recovered + b.Recovered,
		Pruned:    a.Pruned + b.Pruned,
		Missing:   a.Missing + b.Missing,
		Failed:    a.Failed + b.Failed,
	}
}

func (b *MigratingBackend) Shutdown(ctx context.Context) error {
	return errors.Join(b.shared.Shutdown(ctx), b.legacy.Shutdown(ctx))
}

func (b *MigratingBackend) SessionInfo(ctx context.Context, id harness.TerminalID) (SessionInfo, error) {
	provider, err := sessionProvider[SessionInfoProvider](b, id)
	if err != nil {
		return SessionInfo{}, err
	}
	return provider.SessionInfo(ctx, id)
}

func (b *MigratingBackend) SessionLaunchParams(ctx context.Context, id harness.TerminalID) (SessionLaunchParams, error) {
	provider, err := sessionProvider[SessionLaunchParamsProvider](b, id)
	if err != nil {
		return SessionLaunchParams{}, err
	}
	return provider.SessionLaunchParams(ctx, id)
}

func (b *MigratingBackend) ScreenSnapshot(ctx context.Context, id harness.TerminalID) (pty.ScreenSnapshotInfo, error) {
	provider, err := sessionProvider[ScreenSnapshotProvider](b, id)
	if err != nil {
		return pty.ScreenSnapshotInfo{}, err
	}
	return provider.ScreenSnapshot(ctx, id)
}

func (b *MigratingBackend) KittyImage(ctx context.Context, id harness.TerminalID, imageID uint32) (pty.KittyImage, error) {
	provider, err := sessionProvider[KittyImageProvider](b, id)
	if err != nil {
		return pty.KittyImage{}, err
	}
	return provider.KittyImage(ctx, id, imageID)
}

func (b *MigratingBackend) SessionTerminalBuild(id harness.TerminalID) (string, bool) {
	backend, err := b.backendFor(id)
	if err != nil {
		return "", false
	}
	provider, ok := backend.(TerminalBuildProvider)
	if !ok {
		return "", false
	}
	return provider.SessionTerminalBuild(id)
}

func (b *MigratingBackend) UpgradeWorker(ctx context.Context, id harness.TerminalID) error {
	provider, err := sessionProvider[WorkerUpgrader](b, id)
	if err != nil {
		return err
	}
	return provider.UpgradeWorker(ctx, id)
}

func (b *MigratingBackend) SessionLikelyAlive(ctx context.Context, id harness.TerminalID) (bool, error) {
	backend, err := b.backendFor(id)
	if err == nil {
		provider, ok := backend.(SessionLivenessProber)
		if !ok {
			return false, nil
		}
		return provider.SessionLikelyAlive(ctx, id)
	}

	var probeErrs []error
	for _, candidate := range []Backend{b.legacy, b.shared} {
		provider, ok := candidate.(SessionLivenessProber)
		if !ok {
			continue
		}
		alive, probeErr := provider.SessionLikelyAlive(ctx, id)
		if alive {
			return true, nil
		}
		if probeErr != nil {
			probeErrs = append(probeErrs, probeErr)
		}
	}
	return false, errors.Join(probeErrs...)
}

func (b *MigratingBackend) WorkerPIDs(ctx context.Context) map[string]int {
	result := make(map[string]int)
	for _, backend := range []Backend{b.legacy, b.shared} {
		provider, ok := backend.(WorkerProcessProvider)
		if !ok {
			continue
		}
		for id, pid := range provider.WorkerPIDs(ctx) {
			result[id] = pid
		}
	}
	return result
}

func (b *MigratingBackend) backendFor(id harness.TerminalID) (Backend, error) {
	b.mu.RLock()
	owner, ok := b.owners[id]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, id)
	}
	if owner == ownerShared {
		return b.shared, nil
	}
	return b.legacy, nil
}

func sessionProvider[T any](b *MigratingBackend, id harness.TerminalID) (T, error) {
	var zero T
	backend, err := b.backendFor(id)
	if err != nil {
		return zero, err
	}
	provider, ok := backend.(T)
	if !ok {
		return zero, fmt.Errorf("PTY runtime for session %s does not support this operation", id)
	}
	return provider, nil
}

var (
	_ Backend                     = (*MigratingBackend)(nil)
	_ LifecycleHooks              = (*MigratingBackend)(nil)
	_ SessionInfoProvider         = (*MigratingBackend)(nil)
	_ SessionLaunchParamsProvider = (*MigratingBackend)(nil)
	_ WorkerProcessProvider       = (*MigratingBackend)(nil)
	_ ScreenSnapshotProvider      = (*MigratingBackend)(nil)
	_ KittyImageProvider          = (*MigratingBackend)(nil)
	_ TerminalBuildProvider       = (*MigratingBackend)(nil)
	_ WorkerUpgrader              = (*MigratingBackend)(nil)
	_ SessionLivenessProber       = (*MigratingBackend)(nil)
	_ RecoverableRuntime          = (*MigratingBackend)(nil)
)
