package daemon

import (
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func delegateForFirstTurn(t *testing.T, d *Daemon, sourceID string) (*protocol.DelegateResult, error) {
	t.Helper()
	return d.delegateResolved(&resolvedDelegationLaunch{
		Cmd: protocol.CmdDelegate, SourceSessionID: protocol.Ptr(sourceID), Brief: protocol.Ptr("Say hello."),
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
