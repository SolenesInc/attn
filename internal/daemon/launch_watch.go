package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// Receipt: on 2026-09-01, spawn to the harness's first report took 2.6s for
// claude, 4.6s for codex and 2.1s to 4.9s for pi. 90s is a tripwire.
const delegationFirstTurnTimeout = 90 * time.Second

const seedNoteExitScreenMaxBytes = garden.MaxNoteBytes / 2

type launchOutcome struct {
	startedAt   time.Time
	exit        *store.SessionExitScreen
	unconfirmed string
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

// The worker replays `working` for every spawn and a hook's `working` on top of
// it is only observed, so any report from the harness itself is the signal.
func harnessReportedState(source string) bool {
	switch source {
	case stateSourceHook, stateSourceStopHook, stateSourceHookNotify, stateSourceHookStopFailure,
		stateSourceHookCompaction, stateSourceTranscript, stateSourcePluginDriver:
		return true
	}
	return false
}

func (d *Daemon) noteLaunchStarted(sessionID string) {
	d.resolveLaunchWatch(sessionID, launchOutcome{startedAt: time.Now()})
}

func (d *Daemon) noteLaunchExited(info ptybackend.ExitInfo) {
	d.resolveLaunchWatch(info.ID, launchOutcome{exit: d.exitScreenOrBare(info)})
}

func (d *Daemon) exitScreenOrBare(info ptybackend.ExitInfo) *store.SessionExitScreen {
	if exit := d.store.GetSessionExitScreen(info.ID); exit != nil {
		return exit
	}
	return &store.SessionExitScreen{SessionID: info.ID, ExitCode: info.ExitCode, ExitSignal: info.Signal}
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
	timer := time.NewTimer(delegationFirstTurnTimeout)
	defer timer.Stop()
	select {
	case <-watch.done:
		return watch.outcome
	case <-timer.C:
		d.logf("delegation first turn unconfirmed: session=%s no harness report within %s", sessionID, delegationFirstTurnTimeout)
		return launchOutcome{unconfirmed: fmt.Sprintf(
			"no turn reported by the agent within %s; the session is up, `attn agent peek %s` shows its pane",
			delegationFirstTurnTimeout, shortSessionID(sessionID))}
	case <-d.done:
		return launchOutcome{}
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
	if _, err := d.appendSeedNote(seedID, strings.TrimRight(b.String(), "\n"), sessionID, "", garden.NoteKindNote, nil); err != nil {
		d.logf("delegation exit not noted on %s: %v", seedID, err)
		return
	}
	d.ringSeedActivity(seedID, "note", sessionID)
}
