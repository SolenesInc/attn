package ptybackend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/pty"
)

type runtimeOwner uint8

const (
	ownerLegacy runtimeOwner = iota + 1
	ownerShared
)

// MigratingBackend keeps live sessions on the runtime that created them while
// allowing new sessions to move to a replacement runtime.
type MigratingBackend struct {
	legacy Backend
	shared Backend

	mu           sync.RWMutex
	owners       map[string]runtimeOwner
	pendingSpawn map[string]struct{}
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
		owners:       make(map[string]runtimeOwner),
		pendingSpawn: make(map[string]struct{}),
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

// Selection changes affect future admissions only, including explicit reloads.
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

func (b *MigratingBackend) SetStateHandler(handler func(sessionID string, obs pty.Observation)) {
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

func (b *MigratingBackend) Attach(ctx context.Context, sessionID, subscriberID string, opts ...AttachOptions) (AttachInfo, Stream, error) {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return AttachInfo{}, nil, err
	}
	return backend.Attach(ctx, sessionID, subscriberID, opts...)
}

func (b *MigratingBackend) Input(ctx context.Context, sessionID string, data []byte) error {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return err
	}
	return backend.Input(ctx, sessionID, data)
}

func (b *MigratingBackend) Resize(ctx context.Context, sessionID string, cols, rows, xpixel, ypixel uint16) (bool, error) {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return false, err
	}
	return backend.Resize(ctx, sessionID, cols, rows, xpixel, ypixel)
}

func (b *MigratingBackend) SetTheme(ctx context.Context, sessionID string, theme pty.TerminalTheme) error {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return err
	}
	return backend.SetTheme(ctx, sessionID, theme)
}

func (b *MigratingBackend) Kill(ctx context.Context, sessionID string, sig syscall.Signal) error {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return err
	}
	return backend.Kill(ctx, sessionID, sig)
}

func (b *MigratingBackend) Remove(ctx context.Context, sessionID string) error {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return err
	}
	err = backend.Remove(ctx, sessionID)
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) {
		b.mu.Lock()
		delete(b.owners, sessionID)
		b.mu.Unlock()
	}
	return err
}

func (b *MigratingBackend) SessionIDs(_ context.Context) []string {
	b.mu.RLock()
	ids := make([]string, 0, len(b.owners))
	for id := range b.owners {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

func (b *MigratingBackend) Recover(ctx context.Context) (RecoveryReport, error) {
	legacyReport, legacyErr := b.legacy.Recover(ctx)
	sharedReport, sharedErr := b.shared.Recover(ctx)
	report := addRecoveryReports(legacyReport, sharedReport)

	owners := make(map[string]runtimeOwner)
	var conflicts []string
	for _, recovered := range []struct {
		owner   runtimeOwner
		backend Backend
	}{
		{owner: ownerLegacy, backend: b.legacy},
		{owner: ownerShared, backend: b.shared},
	} {
		for _, id := range recovered.backend.SessionIDs(ctx) {
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
		sort.Strings(conflicts)
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

func (b *MigratingBackend) SessionInfo(ctx context.Context, sessionID string) (SessionInfo, error) {
	provider, err := sessionProvider[SessionInfoProvider](b, sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	return provider.SessionInfo(ctx, sessionID)
}

func (b *MigratingBackend) SessionLaunchParams(ctx context.Context, sessionID string) (SessionLaunchParams, error) {
	provider, err := sessionProvider[SessionLaunchParamsProvider](b, sessionID)
	if err != nil {
		return SessionLaunchParams{}, err
	}
	return provider.SessionLaunchParams(ctx, sessionID)
}

func (b *MigratingBackend) ScreenSnapshot(ctx context.Context, sessionID string) (pty.ScreenSnapshotInfo, error) {
	provider, err := sessionProvider[ScreenSnapshotProvider](b, sessionID)
	if err != nil {
		return pty.ScreenSnapshotInfo{}, err
	}
	return provider.ScreenSnapshot(ctx, sessionID)
}

func (b *MigratingBackend) KittyImage(ctx context.Context, sessionID string, imageID uint32) (pty.KittyImage, error) {
	provider, err := sessionProvider[KittyImageProvider](b, sessionID)
	if err != nil {
		return pty.KittyImage{}, err
	}
	return provider.KittyImage(ctx, sessionID, imageID)
}

func (b *MigratingBackend) SessionTerminalBuild(sessionID string) (string, bool) {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return "", false
	}
	provider, ok := backend.(TerminalBuildProvider)
	if !ok {
		return "", false
	}
	return provider.SessionTerminalBuild(sessionID)
}

func (b *MigratingBackend) SessionCanReplayWithFormat(sessionID, format string) bool {
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return false
	}
	provider, ok := backend.(TerminalBuildCompatibilityProvider)
	return ok && provider.SessionCanReplayWithFormat(sessionID, format)
}

func (b *MigratingBackend) UpgradeWorker(ctx context.Context, sessionID string) error {
	provider, err := sessionProvider[WorkerUpgrader](b, sessionID)
	if err != nil {
		return err
	}
	return provider.UpgradeWorker(ctx, sessionID)
}

func (b *MigratingBackend) SessionLikelyAlive(ctx context.Context, sessionID string) (bool, error) {
	backend, err := b.backendFor(sessionID)
	if err == nil {
		provider, ok := backend.(SessionLivenessProber)
		if !ok {
			return false, nil
		}
		return provider.SessionLikelyAlive(ctx, sessionID)
	}

	var probeErrs []error
	for _, candidate := range []Backend{b.legacy, b.shared} {
		provider, ok := candidate.(SessionLivenessProber)
		if !ok {
			continue
		}
		alive, probeErr := provider.SessionLikelyAlive(ctx, sessionID)
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

func (b *MigratingBackend) backendFor(sessionID string) (Backend, error) {
	b.mu.RLock()
	owner, ok := b.owners[sessionID]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", pty.ErrSessionNotFound, sessionID)
	}
	if owner == ownerShared {
		return b.shared, nil
	}
	return b.legacy, nil
}

func sessionProvider[T any](b *MigratingBackend, sessionID string) (T, error) {
	var zero T
	backend, err := b.backendFor(sessionID)
	if err != nil {
		return zero, err
	}
	provider, ok := backend.(T)
	if !ok {
		return zero, fmt.Errorf("PTY runtime for session %s does not support this operation", sessionID)
	}
	return provider, nil
}

var (
	_ Backend                            = (*MigratingBackend)(nil)
	_ LifecycleHooks                     = (*MigratingBackend)(nil)
	_ SessionInfoProvider                = (*MigratingBackend)(nil)
	_ SessionLaunchParamsProvider        = (*MigratingBackend)(nil)
	_ WorkerProcessProvider              = (*MigratingBackend)(nil)
	_ ScreenSnapshotProvider             = (*MigratingBackend)(nil)
	_ KittyImageProvider                 = (*MigratingBackend)(nil)
	_ TerminalBuildProvider              = (*MigratingBackend)(nil)
	_ TerminalBuildCompatibilityProvider = (*MigratingBackend)(nil)
	_ WorkerUpgrader                     = (*MigratingBackend)(nil)
	_ SessionLivenessProber              = (*MigratingBackend)(nil)
	_ RecoverableRuntime                 = (*MigratingBackend)(nil)
)
