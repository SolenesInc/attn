package daemon

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

const delegationFirstTurnTimeout = 90 * time.Second

const seedNoteExitScreenMaxBytes = garden.MaxNoteBytes / 2

var errDelegationInterrupted = errors.New("the daemon stopped before the delegate's first turn")

type launchOutcome struct {
	startedAt   time.Time
	exit        *store.SessionExitScreen
	unconfirmed string
	interrupted bool
}

type launchWatch struct {
	done    chan struct{}
	outcome launchOutcome
}

func (d *Daemon) watchLaunch(sessionID string) *launchWatch {
	watch := &launchWatch{done: make(chan struct{})}
	d.launchWatchMu.Lock()
	if d.launchWatches == nil {
		d.launchWatches = make(map[string]*launchWatch)
	}
	d.launchWatches[sessionID] = watch
	d.launchWatchMu.Unlock()
	return watch
}

func (d *Daemon) forgetLaunchWatch(sessionID string, watch *launchWatch) {
	d.launchWatchMu.Lock()
	if d.launchWatches[sessionID] == watch {
		delete(d.launchWatches, sessionID)
	}
	d.launchWatchMu.Unlock()
}

func (d *Daemon) resolveLaunchWatch(sessionID string, outcome launchOutcome) {
	d.claimLaunchWatch(sessionID).settle(outcome)
}

func (d *Daemon) claimLaunchWatch(sessionID string) *launchWatch {
	d.launchWatchMu.Lock()
	defer d.launchWatchMu.Unlock()
	watch := d.launchWatches[sessionID]
	delete(d.launchWatches, sessionID)
	return watch
}

func (w *launchWatch) settle(outcome launchOutcome) {
	if w != nil {
		w.outcome = outcome
		close(w.done)
	}
}

func harnessReportedState(source string) bool {
	switch source {
	case stateSourceHook, stateSourceStopHook, stateSourceHookNotify, stateSourceHookStopFailure,
		stateSourceHookCompaction, stateSourceTranscript, stateSourcePluginDriver, stateSourceLink:
		return true
	}
	return false
}

func (d *Daemon) noteLaunchStarted(sessionID string) {
	d.resolveLaunchWatch(sessionID, launchOutcome{startedAt: time.Now()})
}

func (d *Daemon) noteLaunchExited(sessionID string, info ptybackend.ExitInfo) {
	d.resolveLaunchWatch(sessionID, launchOutcome{exit: d.exitScreenOrBare(sessionID, info)})
}

func (d *Daemon) exitScreenOrBare(sessionID string, info ptybackend.ExitInfo) *store.SessionExitScreen {
	if exit := d.store.GetSessionExitScreen(sessionID); exit != nil {
		return exit
	}
	return &store.SessionExitScreen{SessionID: sessionID, ExitCode: info.ExitCode, ExitSignal: info.Signal}
}

func (d *Daemon) awaitDelegatedLaunch(sessionID string, watch *launchWatch) launchOutcome {
	defer d.forgetLaunchWatch(sessionID, watch)
	select {
	case <-watch.done:
		return watch.outcome
	default:
	}
	if !d.delegationWaitsForFirstTurn {
		return launchOutcome{}
	}
	wait := delegationFirstTurnTimeout
	if launched := d.store.SessionLaunchedAt(sessionID); !launched.IsZero() {
		wait = min(wait, max(0, wait-time.Since(launched)))
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-watch.done:
		return watch.outcome
	case <-timer.C:
		d.logf("delegation first turn unconfirmed: session=%s no harness report within %s", sessionID, delegationFirstTurnTimeout)
		return launchOutcome{unconfirmed: fmt.Sprintf(
			"no turn reported by the agent within %s; the session is up, `attn agent peek %s` shows its pane",
			delegationFirstTurnTimeout, shortSessionID(sessionID))}
	case <-d.life.Done():
		return launchOutcome{interrupted: true}
	}
}

func delegationExitError(agent, sessionID string, exit *store.SessionExitScreen) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s exited with %s before its first turn; session %s and its pane were kept, `attn agent peek %s` shows what it left",
		agent, describeExit(exit), sessionID, shortSessionID(sessionID))
	if text := strings.TrimRight(exit.Text, "\n"); text != "" {
		b.WriteString("\nscreen at exit:\n")
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

func describeExit(exit *store.SessionExitScreen) string {
	if exit.ExitSignal != "" {
		return fmt.Sprintf("code %d (%s)", exit.ExitCode, exit.ExitSignal)
	}
	return fmt.Sprintf("code %d", exit.ExitCode)
}

func (d *Daemon) noteDelegatedExitOnSeed(seedID, agent, sessionID string, exit *store.SessionExitScreen) {
	if strings.TrimSpace(seedID) == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The delegated agent (%s) exited with %s before starting its first turn, so nothing here was worked on. Session %s and its pane were kept; `attn agent peek %s` shows the screen it left.",
		agent, describeExit(exit), sessionID, shortSessionID(sessionID))
	if text := strings.TrimRight(exit.Text, "\n"); text != "" {
		if len(text) > seedNoteExitScreenMaxBytes {
			text = "[first " + fmt.Sprint(len(text)-seedNoteExitScreenMaxBytes) + " bytes left to peek]\n" + text[len(text)-seedNoteExitScreenMaxBytes:]
		}
		b.WriteString("\n\nScreen at exit:\n\n")
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	if _, err := d.appendSeedNote(seedID, strings.TrimRight(b.String(), "\n"), sessionID, "", garden.NoteKindNote, nil, true, sessionID); err != nil {
		d.logf("delegation exit not noted on %s: %v", seedID, err)
		return
	}
}

func (d *Daemon) watchRecoveredLaunches() {
	records, err := d.store.PendingDelegationOperations()
	if err != nil {
		d.logf("load pending delegation launches: %v", err)
		return
	}
	for i := range records {
		sessionID := records[i].Operation.SessionID
		if d.store.Get(sessionID) == nil {
			continue
		}
		watch := d.watchLaunch(sessionID)
		d.launchWatchMu.Lock()
		if d.recoveredLaunches == nil {
			d.recoveredLaunches = make(map[string]*launchWatch)
		}
		d.recoveredLaunches[sessionID] = watch
		d.launchWatchMu.Unlock()
	}
}

func (d *Daemon) takeRecoveredLaunch(sessionID string) *launchWatch {
	d.launchWatchMu.Lock()
	defer d.launchWatchMu.Unlock()
	watch := d.recoveredLaunches[sessionID]
	delete(d.recoveredLaunches, sessionID)
	return watch
}

func (w *launchWatch) settled() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}
