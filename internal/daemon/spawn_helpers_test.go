package daemon

import (
	"context"
	"fmt"
	"sync"
	"syscall"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type fakeWorkerReconcileBackend struct {
	liveIDs []string
	info    map[string]ptybackend.SessionInfo
	params  map[string]ptybackend.SessionLaunchParams
}

func (b *fakeWorkerReconcileBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return nil
}

func (b *fakeWorkerReconcileBackend) Attach(context.Context, harness.TerminalID, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, nil
}

func (b *fakeWorkerReconcileBackend) Input(context.Context, harness.TerminalID, []byte) error {
	return nil
}

func (b *fakeWorkerReconcileBackend) Resize(context.Context, harness.TerminalID, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}

func (b *fakeWorkerReconcileBackend) SetTheme(context.Context, harness.TerminalID, pty.TerminalTheme) error {
	return nil
}

func (b *fakeWorkerReconcileBackend) Kill(context.Context, harness.TerminalID, syscall.Signal) error {
	return nil
}

func (b *fakeWorkerReconcileBackend) Remove(context.Context, harness.TerminalID) error { return nil }

func (b *fakeWorkerReconcileBackend) TerminalIDs(context.Context) []harness.TerminalID {
	ids := make([]harness.TerminalID, len(b.liveIDs))
	for i, id := range b.liveIDs {
		ids[i] = harness.TerminalID(id)
	}
	return ids
}

func (b *fakeWorkerReconcileBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{Recovered: len(b.liveIDs)}, nil
}

func (b *fakeWorkerReconcileBackend) Shutdown(context.Context) error { return nil }

func (b *fakeWorkerReconcileBackend) SessionInfo(_ context.Context, id harness.TerminalID) (ptybackend.SessionInfo, error) {
	sessionID := string(id)
	info, ok := b.info[sessionID]
	if !ok {
		return ptybackend.SessionInfo{}, fmt.Errorf("missing info for %s", sessionID)
	}
	return info, nil
}

func (b *fakeWorkerReconcileBackend) SessionLaunchParams(_ context.Context, id harness.TerminalID) (ptybackend.SessionLaunchParams, error) {
	params, ok := b.params[string(id)]
	if !ok {
		return ptybackend.SessionLaunchParams{}, pty.ErrSessionNotFound
	}
	return params, nil
}

type fakeOutputStream struct {
	mu         sync.Mutex
	events     chan ptybackend.OutputEvent
	closeCount int
	once       sync.Once
}

func newFakeOutputStream() *fakeOutputStream {
	return &fakeOutputStream{events: make(chan ptybackend.OutputEvent, 8)}
}

func (s *fakeOutputStream) Events() <-chan ptybackend.OutputEvent { return s.events }

func (s *fakeOutputStream) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closeCount++
		s.mu.Unlock()
		close(s.events)
	})
	return nil
}

func (s *fakeOutputStream) ClosedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCount
}

type fakeSpawnBackend struct {
	mu                sync.Mutex
	spawnOpts         []ptybackend.SpawnOptions
	killed            []string
	removed           []string
	onSpawn           func(ptybackend.SpawnOptions)
	onInput           func(string, []byte)
	onInputResult     func(string, []byte) error
	onKill            func()
	spawnErr          error
	sessionIDs        []string
	themeCalls        []pty.TerminalTheme
	themeCallIDs      []string
	setThemeErr       error
	screen            string
	screenUnavailable bool
	onRecover         func()
}

func (b *fakeSpawnBackend) ScreenSnapshot(_ context.Context, _ harness.TerminalID) (pty.ScreenSnapshotInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.screenUnavailable {
		return pty.ScreenSnapshotInfo{}, nil
	}
	text := b.screen
	if text == "" {
		text = "❯"
	}
	return pty.ScreenSnapshotInfo{Screen: &pty.ViewportSnapshot{Text: text, HasText: true}}, nil
}

func (b *fakeSpawnBackend) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	b.mu.Lock()
	b.spawnOpts = append(b.spawnOpts, opts)
	onSpawn := b.onSpawn
	spawnErr := b.spawnErr
	b.mu.Unlock()
	if onSpawn != nil {
		onSpawn(opts)
	}
	return spawnErr
}

func (b *fakeSpawnBackend) Attach(context.Context, harness.TerminalID, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}

func (b *fakeSpawnBackend) Input(_ context.Context, terminal harness.TerminalID, data []byte) error {
	id := string(terminal)
	b.mu.Lock()
	onInput := b.onInput
	onInputResult := b.onInputResult
	b.mu.Unlock()
	if onInput != nil {
		onInput(id, data)
	}
	if onInputResult != nil {
		return onInputResult(id, data)
	}
	return nil
}

func (b *fakeSpawnBackend) Resize(context.Context, harness.TerminalID, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}

func (b *fakeSpawnBackend) SetTheme(_ context.Context, id harness.TerminalID, theme pty.TerminalTheme) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.themeCallIDs = append(b.themeCallIDs, string(id))
	b.themeCalls = append(b.themeCalls, theme)
	return b.setThemeErr
}

func (b *fakeSpawnBackend) Kill(_ context.Context, id harness.TerminalID, _ syscall.Signal) error {
	b.mu.Lock()
	b.killed = append(b.killed, string(id))
	b.mu.Unlock()
	if b.onKill != nil {
		b.onKill()
	}
	return nil
}

func (b *fakeSpawnBackend) Remove(_ context.Context, id harness.TerminalID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, string(id))
	return nil
}

func (b *fakeSpawnBackend) TerminalIDs(context.Context) []harness.TerminalID {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]harness.TerminalID, len(b.sessionIDs))
	for i, id := range b.sessionIDs {
		ids[i] = harness.TerminalID(id)
	}
	return ids
}

func (b *fakeSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	onRecover := b.onRecover
	b.mu.Unlock()
	if onRecover != nil {
		onRecover()
	}
	return ptybackend.RecoveryReport{}, nil
}

func (b *fakeSpawnBackend) Shutdown(context.Context) error { return nil }

func (b *fakeSpawnBackend) LastSpawn() (ptybackend.SpawnOptions, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.spawnOpts) == 0 {
		return ptybackend.SpawnOptions{}, false
	}
	return b.spawnOpts[len(b.spawnOpts)-1], true
}

func (b *fakeSpawnBackend) WasKilledAndRemoved(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	killed := false
	removed := false
	for _, candidate := range b.killed {
		killed = killed || candidate == id
	}
	for _, candidate := range b.removed {
		removed = removed || candidate == id
	}
	return killed && removed
}

func (b *fakeSpawnBackend) RemovedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.removed...)
}

func addTestWorkspace(d *Daemon, id, directory string) {
	rank := d.resolveWorkspaceRank(d.store.GetWorkspace(id))
	d.store.AddWorkspace(&protocol.Workspace{ID: id, Title: id, Directory: directory, Status: protocol.WorkspaceStatusLaunching, Rank: rank})
	d.workspaces.register(id, id, directory, rank, false, false)
}
