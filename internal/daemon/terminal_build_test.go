package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/protocol"
)

func newTerminalBuildDaemon(t *testing.T, backend *fakeSpawnBackend) (*Daemon, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "terminal-build.sock"))
	d.ptyBackend = backend
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      t.TempDir(),
		State:          protocol.SessionStateIdle,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	return d, id
}

func TestTerminalBuild_ConcurrentHellosUpgradeOnlyOnce(t *testing.T) {
	backend := &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
		upgradeEntered:     make(chan string, 1),
		upgradeGate:        make(chan struct{}),
		upgradeDone:        make(chan string, 2),
		onUpgrade:          func(f *fakeSpawnBackend) { f.terminalBuild = buildinfo.SnapshotFormat },
	}
	d, id := newTerminalBuildDaemon(t, backend)

	d.handleTerminalBuildChanged(id, "0123456789ab")
	select {
	case <-backend.upgradeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first hello never started an upgrade")
	}

	d.handleTerminalBuildChanged(id, "0123456789ab")
	close(backend.upgradeGate)
	select {
	case <-backend.upgradeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upgrade never finished")
	}

	select {
	case extra := <-backend.upgradeDone:
		t.Fatalf("a second upgrade ran for %s while the first was in flight", extra)
	case <-time.After(200 * time.Millisecond):
	}
	if got := backend.upgradedSessions(); len(got) != 1 {
		t.Fatalf("upgraded %v, want exactly one swap", got)
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v after a deduped swap, want absent", *clone.TerminalBuildStale)
	}
}
