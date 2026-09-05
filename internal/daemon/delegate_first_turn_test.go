package daemon

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func delegateForFirstTurn(t *testing.T, d *Daemon, sourceID string) (*protocol.DelegateResult, error) {
	t.Helper()
	return d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceID, Brief: "Say hello.",
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("hello"),
	})
}

func queueWorkingPluginReportDuringLaunch(d *Daemon, sessionID string) bool {
	params := pluginReportStateParams{SessionID: sessionID, RunID: "run-1", State: protocol.StateWorking}
	return d.queueReportDuringPluginLaunch(
		&pluginConnection{name: "attn-pi"},
		sessionID,
		pendingPluginReport{State: &params},
	)
}

func TestDelegateFailsWhenTheAgentExitsBeforeItsFirstTurn(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{screen: "Error: Model \"gpt-5.6-sol\" is ambiguous across providers\n"}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceID {
			return
		}
		d.applyState(sessionStateChange{sessionID: opts.ID, state: protocol.StateWorking, cause: liveSignal{}, origin: stateOrigin{source: string(pty.SourceWorkerInfo), detail: "watch subscribe replay"}})
		d.handlePTYExit(ptybackend.ExitInfo{ID: opts.ID, ExitCode: 1})
	}

	result, err := delegateForFirstTurn(t, d, sourceID)
	if err == nil {
		t.Fatalf("delegate() = %+v, want the launch failure", result)
	}
	for _, want := range []string{"codex exited with code 1 before its first turn", "attn agent peek", "is ambiguous across providers"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err.Error(), want)
		}
	}

	var delegated *protocol.Session
	for _, session := range d.store.List("") {
		if session.ID != sourceID {
			delegated = session
		}
	}
	if delegated == nil {
		t.Fatal("the dead delegated session was removed; it is the evidence")
	}
	if pane := d.store.GetSessionExitScreen(delegated.ID); pane == nil || pane.ExitCode != 1 {
		t.Fatalf("exit screen for %s = %+v", delegated.ID, pane)
	}
	seedID, bound := d.gardenDispatchCrown(delegated.ID)
	if !bound {
		t.Fatalf("no seed bound to %s", delegated.ID)
	}
	notes, _, err := d.readNotes(seedID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 || !strings.Contains(notes[0].Body, "exited with code 1 before starting its first turn") ||
		!strings.Contains(notes[0].Body, "is ambiguous across providers") {
		t.Fatalf("seed %s notes = %+v, want the exit explained", seedID, notes)
	}
}

func TestDelegateReportsTheFirstTurnItSaw(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceID {
			return
		}
		d.applyState(sessionStateChange{sessionID: opts.ID, state: protocol.StateWorking, cause: liveSignal{}, origin: stateOrigin{source: string(pty.SourceWorkerInfo), detail: "watch subscribe replay"}})
		d.traceStateEvidence(opts.ID, stateOrigin{source: stateSourceHook}, protocol.StateWorking)
	}

	result, err := delegateForFirstTurn(t, d, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if result.FirstTurnAt == nil || *result.FirstTurnAt == "" {
		t.Fatalf("result = %+v, want first_turn_at", result)
	}
}

func TestDelegateCompletesUnconfirmedWhenNothingWasSeen(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSource(t, d, backend)

	result, err := delegateForFirstTurn(t, d, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if result.FirstTurnAt != nil || result.FirstTurnUnconfirmed != nil {
		t.Fatalf("result = %+v, want neither first_turn_at nor first_turn_unconfirmed", result)
	}
	if d.store.Get(result.SessionID) == nil {
		t.Fatal("delegated session missing")
	}
}

func TestAwaitDelegatedLaunchNamesTheTripwireWhenNothingReports(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &Daemon{delegationWaitsForFirstTurn: true, done: make(chan struct{})}
		watch := d.watchLaunch("0123456789abcdef")
		outcome := d.awaitDelegatedLaunch("0123456789abcdef", watch)
		for _, want := range []string{"no turn reported by the agent within 1m30s", "attn agent peek 01234567"} {
			if !strings.Contains(outcome.unconfirmed, want) {
				t.Fatalf("unconfirmed = %q, want %q", outcome.unconfirmed, want)
			}
		}
	})
}

func TestPluginLaunchExitBeforeReportFailsTheDelegation(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{screen: "Error: Model \"gpt-5.6-sol\" is ambiguous across providers\n"}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceID {
			return
		}
		d.beginPluginSessionLaunch(opts.ID, "attn-pi", "run-1")
		if !d.queueExitDuringPluginLaunch(ptybackend.ExitInfo{ID: opts.ID, ExitCode: 1, LifecycleID: "run-1"}) {
			t.Fatal("exit not queued during the plugin launch")
		}
		if !queueWorkingPluginReportDuringLaunch(d, opts.ID) {
			t.Fatal("report not queued during the plugin launch")
		}
	}

	result, err := delegateForFirstTurn(t, d, sourceID)
	if err == nil {
		t.Fatalf("delegate() = %+v, want the launch failure", result)
	}
	if !strings.Contains(err.Error(), "exited with code 1 before its first turn") || !strings.Contains(err.Error(), "is ambiguous across providers") {
		t.Fatalf("error = %q, want the exit and the screen kept at exit", err.Error())
	}
}

func TestPluginLaunchReportDuringTheExitSnapshotStillFailsTheDelegation(t *testing.T) {
	d := newDelegationDaemon(t)
	backend := &fakeSpawnBackend{screen: "Error: Model \"gpt-5.6-sol\" is ambiguous across providers\n"}
	_, sourceID, _ := setupDelegationSource(t, d, backend)
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == sourceID {
			return
		}
		d.beginPluginSessionLaunch(opts.ID, "attn-pi", "run-1")
		var once sync.Once
		backend.mu.Lock()
		backend.onSnapshot = func() {
			once.Do(func() {
				reported := make(chan struct{})
				go func() {
					defer close(reported)
					queueWorkingPluginReportDuringLaunch(d, opts.ID)
				}()
				<-reported
			})
		}
		backend.mu.Unlock()
		if !d.queueExitDuringPluginLaunch(ptybackend.ExitInfo{ID: opts.ID, ExitCode: 1, LifecycleID: "run-1"}) {
			t.Fatal("exit not queued during the plugin launch")
		}
	}

	result, err := delegateForFirstTurn(t, d, sourceID)
	if err == nil {
		t.Fatalf("delegate() = %+v, want the launch failure", result)
	}
	if !strings.Contains(err.Error(), "exited with code 1 before its first turn") || !strings.Contains(err.Error(), "is ambiguous across providers") {
		t.Fatalf("error = %q, want the exit and the screen kept at exit", err.Error())
	}
}

func TestPluginLaunchMetadataAloneLeavesTheWatchOpen(t *testing.T) {
	d := newDelegationDaemon(t)
	d.beginPluginSessionLaunch("s1", "attn-pi", "run-1")
	watch := d.watchLaunch("s1")
	plugin := &pluginConnection{name: "attn-pi"}
	if !d.queueReportDuringPluginLaunch(plugin, "s1", pendingPluginReport{Metadata: &pluginReportMetadataParams{SessionID: "s1", RunID: "run-1"}}) {
		t.Fatal("metadata report not queued")
	}
	select {
	case <-watch.done:
		t.Fatalf("metadata alone settled the watch: %+v", watch.outcome)
	default:
	}
	if !d.queueExitDuringPluginLaunch(ptybackend.ExitInfo{ID: "s1", ExitCode: 1, LifecycleID: "run-1"}) {
		t.Fatal("exit not queued")
	}
	outcome := d.awaitDelegatedLaunch("s1", watch)
	if outcome.exit == nil || outcome.exit.ExitCode != 1 {
		t.Fatalf("outcome = %+v, want the exit after a metadata-only report", outcome)
	}
}
