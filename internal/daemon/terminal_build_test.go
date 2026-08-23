package daemon

import (
	"errors"
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

func TestTerminalBuild_MatchingWorkerIsNotStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{
		terminalBuild:      buildinfo.SnapshotFormat,
		terminalBuildKnown: true,
	})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v for a same-build worker, want absent", *clone.TerminalBuildStale)
	}
}

func TestTerminalBuild_DifferentWorkerIsStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
	})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent for a worker built against a different libghostty-vt")
	}
}

// The case the first bump after this change actually produces: every worker
// alive today predates the field and reports nothing at all.
func TestTerminalBuild_SilentWorkerIsStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{terminalBuildKnown: true})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent for a worker that reported no format")
	}
}

// No handshake has landed yet. That is not a verdict, and guessing one would
// flash the notice at every session on every daemon start.
func TestTerminalBuild_UnansweredWorkerIsNotStale(t *testing.T) {
	d, id := newTerminalBuildDaemon(t, &fakeSpawnBackend{})

	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v before the worker answered, want absent", *clone.TerminalBuildStale)
	}
}

// The promise the reload notice makes: a bump swaps the worker underneath and
// the user sees nothing. The notice is the fallback, not the plan.
func TestTerminalBuild_MismatchSwapsTheWorkerInPlace(t *testing.T) {
	backend := &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
		upgradeDone:        make(chan string, 1),
		// What the real re-handshake does: the new image reports its own build.
		onUpgrade: func(f *fakeSpawnBackend) { f.terminalBuild = buildinfo.SnapshotFormat },
	}
	d, id := newTerminalBuildDaemon(t, backend)

	d.handleTerminalBuildChanged(id, "0123456789ab")
	select {
	case <-backend.upgradeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a mismatched worker was never asked to upgrade")
	}

	if got := backend.upgradedSessions(); len(got) != 1 || got[0] != id {
		t.Fatalf("upgraded %v, want exactly [%s]", got, id)
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.TerminalBuildStale != nil {
		t.Fatalf("terminal_build_stale = %v after a successful swap, want absent", *clone.TerminalBuildStale)
	}
}

func TestTerminalBuild_FailedSwapFallsBackToTheReloadNotice(t *testing.T) {
	backend := &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
		upgradeDone:        make(chan string, 1),
		upgradeErr:         errors.New("worker did not answer"),
	}
	d, id := newTerminalBuildDaemon(t, backend)

	d.handleTerminalBuildChanged(id, "0123456789ab")
	select {
	case <-backend.upgradeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a mismatched worker was never asked to upgrade")
	}

	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent after the swap failed; the user gets no way out")
	}
}

// The bug this pins cost a live verification: the handshake that reports the
// format runs while the backend is still probing the recovered worker, so the
// session is not in its map yet and reading the format back answers "unknown".
// A daemon that re-derives it there upgrades nothing, silently.
func TestTerminalBuild_UpgradesOnTheReportedFormatNotAReadBack(t *testing.T) {
	backend := &fakeSpawnBackend{
		// What the backend answers mid-handshake: nothing known yet.
		terminalBuildKnown: false,
		upgradeDone:        make(chan string, 1),
		onUpgrade: func(f *fakeSpawnBackend) {
			f.terminalBuild = buildinfo.SnapshotFormat
			f.terminalBuildKnown = true
		},
	}
	d, id := newTerminalBuildDaemon(t, backend)

	d.handleTerminalBuildChanged(id, "0123456789ab")
	select {
	case <-backend.upgradeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a mismatched worker was never asked to upgrade; the format it reported was ignored")
	}
}

// A daemon start handshakes the same worker twice — the recovery probe and the
// lifecycle watch — and both hellos report a format nothing has recorded yet.
// Before this, both upgraded: one won and the other was told an upgrade was
// already running, which published the stale flag and flashed a reload notice
// at a session that had just been swapped.
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

	// The second hello arrives while the first swap is still running.
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

// The way out. Past the capture a worker cannot hand the session back, so a
// bump that makes the swap misbehave has to be answerable with an env var and a
// daemon restart rather than a release.
func TestTerminalBuild_UpgradeCanBeTurnedOff(t *testing.T) {
	t.Setenv(inplaceUpgradeEnvVar, "0")
	backend := &fakeSpawnBackend{
		terminalBuild:      "0123456789ab",
		terminalBuildKnown: true,
		upgradeDone:        make(chan string, 1),
	}
	d, id := newTerminalBuildDaemon(t, backend)

	d.handleTerminalBuildChanged(id, "0123456789ab")
	select {
	case <-backend.upgradeDone:
		t.Fatal("a worker was upgraded with the swap turned off")
	case <-time.After(300 * time.Millisecond):
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if !protocol.Deref(clone.TerminalBuildStale) {
		t.Fatal("terminal_build_stale absent with the swap off; the user gets no way out at all")
	}
}
