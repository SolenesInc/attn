package ptybackend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/pty"
)

type migrationTestBackend struct {
	mu          sync.Mutex
	ids         map[string]struct{}
	spawned     []string
	inputs      map[string][][]byte
	report      RecoveryReport
	recover     error
	shutdown    error
	beforeSpawn func()
	probe       func(context.Context) error
	removeErr   error
}

func newMigrationTestBackend(ids ...string) *migrationTestBackend {
	b := &migrationTestBackend{ids: make(map[string]struct{}), inputs: make(map[string][][]byte)}
	for _, id := range ids {
		b.ids[id] = struct{}{}
	}
	return b
}

func (b *migrationTestBackend) Spawn(_ context.Context, opts SpawnOptions) error {
	if b.beforeSpawn != nil {
		b.beforeSpawn()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids[opts.ID] = struct{}{}
	b.spawned = append(b.spawned, opts.ID)
	return nil
}

func (b *migrationTestBackend) Probe(ctx context.Context) error {
	return b.probe(ctx)
}

func TestMigratingBackendToggleKeepsExistingAndPendingOwners(t *testing.T) {
	legacy, shared := newMigrationTestBackend(), newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, false)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	legacy.beforeSpawn = func() { close(started); <-release }
	done := make(chan error, 1)
	go func() { done <- backend.Spawn(context.Background(), SpawnOptions{ID: "pending-legacy"}) }()
	<-started
	backend.SetSharedForNewSessions(true)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	legacy.beforeSpawn = nil
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: "shared"}); err != nil {
		t.Fatal(err)
	}
	backend.SetSharedForNewSessions(false)
	if backend.SharedForNewSessions() {
		t.Fatal("selection stayed enabled")
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: "legacy"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pending-legacy", "shared", "legacy"} {
		if err := backend.Input(context.Background(), id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(legacy.spawned, []string{"pending-legacy", "legacy"}) || !reflect.DeepEqual(shared.spawned, []string{"shared"}) {
		t.Fatalf("unexpected launches: legacy=%v shared=%v", legacy.spawned, shared.spawned)
	}
	if len(legacy.inputs["pending-legacy"]) != 1 || len(legacy.inputs["legacy"]) != 1 || len(shared.inputs["shared"]) != 1 {
		t.Fatal("toggle moved a session to a different owner")
	}
}

func TestMigratingBackendProbeDoesNotBlockExistingIO(t *testing.T) {
	legacy, shared := newMigrationTestBackend("running"), newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	shared.probe = func(context.Context) error { close(started); <-release; return nil }
	done := make(chan error, 1)
	go func() { done <- backend.ProbeShared(context.Background()) }()
	<-started
	if err := backend.Input(context.Background(), "running", []byte("still-alive")); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (b *migrationTestBackend) Attach(context.Context, string, string, ...AttachOptions) (AttachInfo, Stream, error) {
	return AttachInfo{}, nil, nil
}

func (b *migrationTestBackend) Input(_ context.Context, id string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inputs[id] = append(b.inputs[id], append([]byte(nil), data...))
	return nil
}

func (b *migrationTestBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (bool, error) {
	return true, nil
}

func (b *migrationTestBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}

func (b *migrationTestBackend) Kill(context.Context, string, syscall.Signal) error {
	return nil
}

func (b *migrationTestBackend) Remove(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.removeErr != nil {
		return b.removeErr
	}
	delete(b.ids, id)
	return nil
}

func TestMigratingBackendMissingLegacySocketDoesNotBlockReplacement(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	legacy, err := NewWorker(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-missing-worker", BinaryPath: "/bin/true",
	})
	if err != nil {
		t.Fatal(err)
	}
	const id = "reload-after-exit"
	legacy.sessions[id] = &workerSession{
		SessionID: id, SocketPath: filepath.Join(root, "gone.sock"),
		RegistryPath: filepath.Join(root, "gone.json"),
	}
	shared := newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	backend.owners[id] = ownerLegacy
	ctx := context.Background()
	if err := backend.Remove(ctx, id); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove() = %v, want missing worker socket", err)
	}
	if ids := legacy.SessionIDs(ctx); len(ids) != 0 {
		t.Fatalf("legacy still owns %v", ids)
	}
	if err := backend.Spawn(ctx, SpawnOptions{ID: id}); err != nil {
		t.Fatalf("replacement spawn: %v", err)
	}
	if !reflect.DeepEqual(shared.spawned, []string{id}) {
		t.Fatalf("replacement launches = %v", shared.spawned)
	}
}

func TestMigratingBackendFailedRemovalKeepsOwner(t *testing.T) {
	legacy := newMigrationTestBackend("running")
	legacy.removeErr = errors.New("worker is busy")
	shared := newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := backend.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := backend.Remove(ctx, "running"); !errors.Is(err, legacy.removeErr) {
		t.Fatalf("Remove() = %v, want original failure", err)
	}
	if err := backend.Spawn(ctx, SpawnOptions{ID: "running"}); err == nil {
		t.Fatal("spawn replaced a worker whose removal failed")
	}
	if err := backend.Input(ctx, "running", []byte("still-owned")); err != nil {
		t.Fatal(err)
	}
	if len(legacy.inputs["running"]) != 1 || len(shared.spawned) != 0 {
		t.Fatal("failed removal changed the session owner")
	}
}

func (b *migrationTestBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.ids))
	for id := range b.ids {
		ids = append(ids, id)
	}
	return ids
}

func (b *migrationTestBackend) Recover(context.Context) (RecoveryReport, error) {
	return b.report, b.recover
}

func (b *migrationTestBackend) Shutdown(context.Context) error {
	return b.shutdown
}

func (b *migrationTestBackend) SessionInfo(_ context.Context, id string) (SessionInfo, error) {
	return SessionInfo{SessionID: id}, nil
}

func (b *migrationTestBackend) SessionLaunchParams(context.Context, string) (SessionLaunchParams, error) {
	return SessionLaunchParams{Recorded: true}, nil
}

func (b *migrationTestBackend) ScreenSnapshot(context.Context, string) (pty.ScreenSnapshotInfo, error) {
	return pty.ScreenSnapshotInfo{Running: true}, nil
}

func (b *migrationTestBackend) KittyImage(context.Context, string, uint32) (pty.KittyImage, error) {
	return pty.KittyImage{}, nil
}

func (b *migrationTestBackend) SessionTerminalBuild(string) (string, bool) {
	return "test-format", true
}

func (b *migrationTestBackend) UpgradeWorker(context.Context, string) error {
	return nil
}

func (b *migrationTestBackend) SessionLikelyAlive(_ context.Context, id string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.ids[id]
	return ok, nil
}

func (b *migrationTestBackend) WorkerPIDs(context.Context) map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make(map[string]int, len(b.ids))
	for id := range b.ids {
		result[id] = 42
	}
	return result
}

func TestMigratingBackendRecoversBothOwnersWithoutMovingSessions(t *testing.T) {
	legacy := newMigrationTestBackend("legacy-session")
	shared := newMigrationTestBackend("shared-session")
	legacy.report = RecoveryReport{Recovered: 1}
	shared.report = RecoveryReport{Recovered: 1}

	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Recovered != 2 {
		t.Fatalf("Recover().Recovered = %d, want 2", report.Recovered)
	}

	if err := backend.Input(context.Background(), "legacy-session", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := backend.Input(context.Background(), "shared-session", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got := string(legacy.inputs["legacy-session"][0]); got != "old" {
		t.Fatalf("legacy input = %q, want old", got)
	}
	if got := string(shared.inputs["shared-session"][0]); got != "new" {
		t.Fatalf("shared input = %q, want new", got)
	}
}

func TestMigratingBackendRoutesOnlyNewSessionsToShared(t *testing.T) {
	legacy := newMigrationTestBackend("already-running")
	shared := newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: "created-now"}); err != nil {
		t.Fatal(err)
	}

	if len(legacy.spawned) != 0 {
		t.Fatalf("legacy spawned = %v, want none", legacy.spawned)
	}
	if !reflect.DeepEqual(shared.spawned, []string{"created-now"}) {
		t.Fatalf("shared spawned = %v, want [created-now]", shared.spawned)
	}
	if got := backend.SessionIDs(context.Background()); !reflect.DeepEqual(got, []string{"already-running", "created-now"}) {
		t.Fatalf("SessionIDs() = %v", got)
	}
}

func TestMigratingBackendCanKeepNewSessionsOnLegacyWhenSharedProbeFails(t *testing.T) {
	legacy := newMigrationTestBackend()
	shared := newMigrationTestBackend()
	backend, err := NewMigrating(legacy, shared, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: "fallback"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy.spawned, []string{"fallback"}) {
		t.Fatalf("legacy spawned = %v, want [fallback]", legacy.spawned)
	}
	if len(shared.spawned) != 0 {
		t.Fatalf("shared spawned = %v, want none", shared.spawned)
	}
}

func TestMigratingBackendRejectsConflictingRecoveryOwnership(t *testing.T) {
	legacy := newMigrationTestBackend("same-session")
	shared := newMigrationTestBackend("same-session")
	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}

	report, err := backend.Recover(context.Background())
	if err == nil {
		t.Fatal("Recover() error = nil, want ownership conflict")
	}
	if report.Failed != 1 {
		t.Fatalf("Recover().Failed = %d, want 1", report.Failed)
	}
	if _, err := backend.SessionInfo(context.Background(), "same-session"); !errors.Is(err, pty.ErrSessionNotFound) {
		t.Fatalf("SessionInfo() error = %v, want session not found", err)
	}
}

func TestMigratingBackendUnknownLivenessChecksBothRegistries(t *testing.T) {
	legacy := newMigrationTestBackend()
	shared := newMigrationTestBackend("stranded-shared-session")
	backend, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}

	alive, err := backend.SessionLikelyAlive(context.Background(), "stranded-shared-session")
	if err != nil {
		t.Fatal(err)
	}
	if !alive {
		t.Fatal("SessionLikelyAlive() = false, want true from shared registry")
	}
}
