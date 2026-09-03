package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func newAutoSettleDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	return d, seedAutoSettleSession(t, d, t.TempDir())
}

// A synctest bubble's clock starts at 2000-01-01, so a turn opened outside it is stamped
// decades ahead of every settle made inside: build the daemon outside, call this inside.
func seedAutoSettleSession(t *testing.T, d *Daemon, dir string) string {
	t.Helper()
	id := "session"
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentCodex,
		Directory:      dir,
		State:          protocol.SessionStateWaitingInput,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.SetSetting(SettingAutoSettleCountdownSeconds, "3600")
	if !d.store.OpenTurnIfClosed(id, time.Now()) {
		t.Fatal("OpenTurnIfClosed() = false; the fixture owes no turn")
	}
	creditUserInput(t, d, id)
	return id
}

func creditUserInput(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	lane := d.sessionInputs().lane(sessionID)
	lane.mu.Lock()
	lane.userGeneration++
	lane.userSubmit = true
	lane.mu.Unlock()
	d.observePromptTaken(sessionID, "user steer", time.Now())
}

func creditUserInputForNextWorking(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	lane := d.sessionInputs().lane(sessionID)
	lane.mu.Lock()
	lane.userGeneration++
	lane.userSubmit = true
	lane.mu.Unlock()
	d.sessionInputs().observePromptTaken(sessionID, "user steer", time.Now())
}

func autoSettlePending(d *Daemon, sessionID string) (*autoSettleTimer, bool) {
	d.autoSettleMu.Lock()
	defer d.autoSettleMu.Unlock()
	entry, ok := d.autoSettleTimers[sessionID]
	return entry, ok
}

func fireAutoSettleNow(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	entry, ok := autoSettlePending(d, sessionID)
	if !ok {
		t.Fatalf("no auto-settle pending for %s", sessionID)
	}
	entry.timer.Stop()
	d.autoSettleFire(sessionID, entry.timer)
}

func turnIsOwed(d *Daemon, sessionID string) bool {
	return d.turnOwed(sessionID)
}

func TestAutoSettle_ArmsThenCountsDownThenSettles(t *testing.T) {
	d, id := newAutoSettleDaemon(t)

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no auto-settle armed after the session went to work")
	}
	if entry.phase != autoSettleArming {
		t.Fatalf("phase = %v, want arming", entry.phase)
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q during arming, want absent", *clone.AutoSettleFiresAt)
	}

	fireAutoSettleNow(t, d, id)

	entry, ok = autoSettlePending(d, id)
	if !ok || entry.phase != autoSettleCounting {
		t.Fatalf("after the arm delay: pending=%v entry=%+v, want a counting phase", ok, entry)
	}
	clone = d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt == nil {
		t.Fatal("auto_settle_fires_at absent while counting; the client has nothing to animate")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled during the countdown; it must still be owed until the countdown ends")
	}

	fireAutoSettleNow(t, d, id)

	if turnIsOwed(d, id) {
		t.Fatal("turn still owed after the countdown elapsed; want settled")
	}
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("a timer is still pending after the settle")
	}
	clone = d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q after settling, want absent", *clone.AutoSettleFiresAt)
	}
}

func TestAutoSettle_LeavingWorkingAborts(t *testing.T) {
	for _, state := range []string{
		protocol.StateWaitingInput,
		protocol.StatePendingApproval,
		protocol.StateUnknown,
		protocol.StateIdle,
		string(protocol.SessionStateRecoverable),
		protocol.StateScheduled,
		protocol.StateLaunching,
	} {
		t.Run(state, func(t *testing.T) {
			d, id := newAutoSettleDaemon(t)
			if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
				t.Fatal("applyState(working) = false")
			}
			fireAutoSettleNow(t, d, id)
			if _, ok := autoSettlePending(d, id); !ok {
				t.Fatal("no countdown to abort")
			}

			if !d.applyState(sessionStateChange{sessionID: id, state: state, cause: liveSignal{}}) {
				t.Fatalf("applyState(%s) = false", state)
			}

			if _, ok := autoSettlePending(d, id); ok {
				t.Fatalf("countdown survived the move to %s; the agent wants the user and its turn would be buried", state)
			}
			if !turnIsOwed(d, id) {
				t.Fatalf("turn was settled on the move to %s", state)
			}
		})
	}
}

func TestAutoSettle_ReReportedWorkingKeepsTheSameTimer(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	first, _ := autoSettlePending(d, id)

	for i := 0; i < 3; i++ {
		d.syncAutoSettle(id, protocol.StateWorking)
	}

	again, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("timer disappeared on a re-reported working")
	}
	if again.timer != first.timer || !again.firesAt.Equal(first.firesAt) {
		t.Fatalf("timer was replaced on a re-reported working: deadline %v -> %v", first.firesAt, again.firesAt)
	}
}

func TestAutoSettle_CancelKeepsTheTurnAndDoesNotReArm(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived the cancel")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled by the cancel")
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q after cancel, want absent", *clone.AutoSettleFiresAt)
	}

	d.syncAutoSettle(id, protocol.StateWorking)
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("re-armed while the session kept working; the cancel must stand")
	}

	d.syncAutoSettle(id, protocol.StateWaitingInput)
	creditUserInputForNextWorking(t, d, id)
	d.syncAutoSettle(id, protocol.StateWorking)
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no fresh arm after the session left and re-entered working")
	}
}

func dismissArmed(d *Daemon, sessionID string) bool {
	return protocol.Deref(d.sessionForBroadcast(d.store.Get(sessionID)).AutoSettleDismissArmed)
}

func TestAutoSettle_ArmedBeforeTheSteerDismissesTheNextSettle(t *testing.T) {
	d, id := newAutoSettleDaemon(t)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if !dismissArmed(d, id) {
		t.Fatal("no standing dismissal after pressing with nothing counting down")
	}
	d.syncAutoSettle(id, protocol.StateWaitingInput)
	if !dismissArmed(d, id) {
		t.Fatal("the dismissal was retired by a re-reported waiting_input, before it covered anything")
	}

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("the steer armed a settle the user had already dismissed")
	}
	if !dismissArmed(d, id) {
		t.Fatal("the dismissal did not survive into the stretch it answers")
	}

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWaitingInput, cause: liveSignal{}}) {
		t.Fatal("applyState(waiting_input) = false")
	}
	if dismissArmed(d, id) {
		t.Fatal("the dismissal outlived the working stretch it was spent on")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("the turn was settled despite a standing dismissal")
	}
	creditUserInputForNextWorking(t, d, id)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no fresh arm on the next steer; the dismissal answered one settle, not all of them")
	}
}

func TestAutoSettle_PressingAgainDisarms(t *testing.T) {
	d, id := newAutoSettleDaemon(t)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})
	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if dismissArmed(d, id) {
		t.Fatal("pressing again did not disarm the standing dismissal")
	}
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no settle armed after the disarm; the steer must be back to ordinary")
	}
}

func TestAutoSettle_DisarmingWhileWorkingReArmsTheSettle(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})
	if !dismissArmed(d, id) {
		t.Fatal("cancelling a running countdown left no standing dismissal")
	}

	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

	if dismissArmed(d, id) {
		t.Fatal("the standing dismissal survived the disarm")
	}
	entry, ok := autoSettlePending(d, id)
	if !ok || entry.phase != autoSettleArming {
		t.Fatalf("after disarming mid-stretch: pending=%v entry=%+v, want the arm delay back", ok, entry)
	}
}

func TestAutoSettle_NothingToDismissDoesNotArm(t *testing.T) {
	t.Run("feature off", func(t *testing.T) {
		d, id := newAutoSettleDaemon(t)
		d.store.SetSetting(SettingAutoSettleEnabled, "false")

		d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})

		if dismissArmed(d, id) {
			t.Fatal("armed a dismissal with auto-settle switched off")
		}
	})

	t.Run("session outside the queue", func(t *testing.T) {
		d, _ := newAutoSettleDaemon(t)
		shellID := "shell"
		d.store.Add(&protocol.Session{
			ID:        shellID,
			Label:     shellID,
			Agent:     protocol.SessionAgentShell,
			Directory: t.TempDir(),
			State:     protocol.SessionStateIdle,
		})

		d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: shellID})

		if dismissArmed(d, shellID) {
			t.Fatal("armed a dismissal on a shell; a session outside the queue never auto-settles")
		}
	})
}

func TestAutoSettle_DisablingClearsAStandingDismissal(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	d.handleCancelCountdown(&protocol.CancelCountdownMessage{SessionID: id})
	if !dismissArmed(d, id) {
		t.Fatal("precondition: nothing armed")
	}

	d.cancelAllAutoSettle()

	if dismissArmed(d, id) {
		t.Fatal("a standing dismissal survived auto-settle being switched off")
	}
}

func TestAutoSettle_DisabledNeverArms(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	d.store.SetSetting(SettingAutoSettleEnabled, "false")

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("armed with auto-settle disabled")
	}
}

func TestAutoSettle_DisablingCancelsARunningCountdown(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)
	if _, ok := autoSettlePending(d, id); !ok {
		t.Fatal("no countdown to cancel")
	}

	d.handleSetSettingWS(&wsClient{}, &protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   SettingAutoSettleEnabled,
		Value: "false",
	})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived turning the feature off")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled by turning the feature off")
	}
}

func TestAutoSettle_NoTurnOwedNeverArms(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
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
	d.store.SetSetting(SettingAutoSettleEnabled, "true")
	d.store.SetSetting(SettingAutoSettleArmSeconds, "3600")
	d.store.UpdateState(id, protocol.StateWorking)
	d.syncAutoSettle(id, protocol.StateWorking)

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("armed for a session that owes no turn")
	}
}

func TestAutoSettle_ManualSettleCancelsTheCountdown(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.handleSettleTurn(&protocol.SettleTurnMessage{SessionID: id})

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived a manual settle")
	}
	if turnIsOwed(d, id) {
		t.Fatal("manual settle did not close the turn")
	}
}

func TestAutoSettle_FireTimeRecheckRefusesANonWorkingSession(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	outcomes := make(chan string, 1)
	d.autoSettleFireHook = func(_, outcome string) { outcomes <- outcome }
	d.store.UpdateState(id, protocol.StateWaitingInput)

	fireAutoSettleNow(t, d, id)

	if got := <-outcomes; got != "not-working" {
		t.Fatalf("outcome = %q, want %q", got, "not-working")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled despite the session no longer working")
	}
}

func TestAutoSettle_ClassificationSuspendsAndThenReevaluates(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.recordClassifierStarted(id, time.Now())

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("countdown survived classification start")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("classification start settled the turn")
	}

	d.recordClassifierFinished(id)
	entry, ok := autoSettlePending(d, id)
	if !ok || entry.phase != autoSettleArming {
		t.Fatalf("after classification: pending=%v entry=%+v, want a fresh arm", ok, entry)
	}
}

func TestAutoSettle_FireTimeRecheckRefusesHeldWorkingDuringClassification(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.recordEvidence(id, time.Now(), func(e *sessionstate.Evidence) {
		e.ClassifyingSince = time.Now()
	})

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no countdown to exercise the fire-time classification gate")
	}
	entry.timer.Stop()
	d.autoSettleFire(id, entry.timer)

	if !turnIsOwed(d, id) {
		t.Fatal("turn settled while the working state was held for classification")
	}
	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("timer survived a classifying fire-time refusal")
	}
}

func TestAutoSettle_RealTimersSettleTheTurn(t *testing.T) {
	d := newBubbleDaemon(t)
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := seedAutoSettleSession(t, d, dir)
		d.store.SetSetting(SettingAutoSettleArmSeconds, "")
		d.store.SetSetting(SettingAutoSettleCountdownSeconds, "")

		if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
			t.Fatal("applyState(working) = false")
		}

		time.Sleep(defaultAutoSettleArmSeconds * time.Second)
		synctest.Wait()
		if !turnIsOwed(d, id) {
			t.Fatal("the turn settled at the end of the arm delay, before its countdown ran")
		}

		time.Sleep(defaultAutoSettleCountdownSeconds * time.Second)
		synctest.Wait()
		if turnIsOwed(d, id) {
			t.Fatal("the turn was never auto-settled although both windows elapsed")
		}
	})
}

func TestValidateAutoSettleSeconds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{name: "blank arm means default", key: SettingAutoSettleArmSeconds, value: ""},
		{name: "arm in range", key: SettingAutoSettleArmSeconds, value: "30"},
		{name: "arm below floor", key: SettingAutoSettleArmSeconds, value: "1", wantErr: true},
		{name: "arm above ceiling", key: SettingAutoSettleArmSeconds, value: "99999", wantErr: true},
		{name: "arm not a number", key: SettingAutoSettleArmSeconds, value: "soon", wantErr: true},
		{name: "countdown in range", key: SettingAutoSettleCountdownSeconds, value: "15"},
		{name: "countdown below floor", key: SettingAutoSettleCountdownSeconds, value: "1", wantErr: true},
		{name: "enabled accepts a boolean", key: SettingAutoSettleEnabled, value: "true"},
		{name: "enabled rejects a number", key: SettingAutoSettleEnabled, value: "30", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
			err := d.validateSetting(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSetting(%q, %q) = %v, wantErr %v", tc.key, tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestAutoSettleSettingsSurfaceEffectiveDefaults(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock"))
	settings := d.settingsWithAgentAvailability()

	for key, want := range map[string]string{
		SettingAutoSettleEnabled:          "false",
		SettingAutoSettleArmSeconds:       "30",
		SettingAutoSettleCountdownSeconds: "15",
	} {
		if got := settings[key]; got != want {
			t.Errorf("settings[%q] = %v, want %q", key, got, want)
		}
	}
}

// A goroutine blocked on a sync.Mutex is not durably blocked, so a bubble has no
// instant at which to call the race staged; this hook stands in for it.
func TestAutoSettle_ConcurrentApprovalKeepsTheTurn(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	entry, ok := autoSettlePending(d, id)
	if !ok {
		t.Fatal("no countdown to race against")
	}

	approvalDone := make(chan struct{})
	release := make(chan struct{})
	go func() {
		defer close(approvalDone)
		<-release
		d.applyState(sessionStateChange{
			sessionID: id,
			state:     protocol.StatePendingApproval,
			cause:     liveSignal{},
		})
	}()

	d.autoSettlePreSettleHook = func() {
		close(release)
		time.Sleep(100 * time.Millisecond)
	}

	d.autoSettleFire(id, entry.timer)
	<-approvalDone

	if state := string(d.store.Get(id).State); state != protocol.StatePendingApproval {
		t.Fatalf("state = %s, want pending_approval", state)
	}
	if !d.turnOwed(id) {
		t.Fatal("the turn was settled while the session was asking for approval")
	}
}

func typeInto(d *Daemon, sessionID string) {
	typeIntoAs(d, sessionID, "")
}

func typeIntoAs(d *Daemon, sessionID, source string) {
	msg := &protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: sessionID, Data: "x"}
	if source != "" {
		msg.Source = protocol.Ptr(source)
	}
	d.handlePtyInput(nil, msg)
}

func goQuiet(d *Daemon, sessionID string) {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	d.lastAutoSettleActivityAt[sessionID] = time.Now().Add(-2 * autoSettleHoldQuietWindow)
}

func movePointerIn(d *Daemon, sessionID string) {
	d.handleTerminalPointerActivity(&protocol.TerminalPointerActivityMessage{
		Cmd: protocol.CmdTerminalPointerActivity,
		ID:  sessionID,
	})
}

func TestAutoSettle_TypingFreezesTheCountdownAndQuietResumesIt(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)
	counting, _ := autoSettlePending(d, id)

	typeInto(d, id)

	held, ok := autoSettlePending(d, id)
	if !ok || held.phase != autoSettleHeld {
		t.Fatalf("after a keystroke: pending=%v entry=%+v, want the held phase", ok, held)
	}
	if held.resume != autoSettleCounting {
		t.Fatalf("resume phase = %v, want counting", held.resume)
	}
	if held.timer == counting.timer {
		t.Fatal("the countdown's own timer is still running; it would settle the turn mid-keystroke")
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q while held; a frozen countdown has no deadline to animate against", *clone.AutoSettleFiresAt)
	}
	if !protocol.Deref(clone.AutoSettleHeld) {
		t.Fatal("auto_settle_held absent while frozen; the tile has nothing to freeze on")
	}

	fireAutoSettleNow(t, d, id)
	again, ok := autoSettlePending(d, id)
	if !ok || again.phase != autoSettleHeld {
		t.Fatalf("quiet check with a fresh keystroke: pending=%v entry=%+v, want still held", ok, again)
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled while the user was still typing")
	}

	goQuiet(d, id)
	fireAutoSettleNow(t, d, id)

	resumed, ok := autoSettlePending(d, id)
	if !ok || resumed.phase != autoSettleCounting {
		t.Fatalf("after the quiet window: pending=%v entry=%+v, want counting again", ok, resumed)
	}
	if window := time.Until(resumed.firesAt); window < 3500*time.Second {
		t.Fatalf("resumed countdown = %v, want the full configured window", window)
	}
	clone = d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleFiresAt == nil {
		t.Fatal("auto_settle_fires_at absent after the hold released")
	}
	if clone.AutoSettleHeld != nil {
		t.Fatal("auto_settle_held still set after the hold released")
	}

	fireAutoSettleNow(t, d, id)
	if turnIsOwed(d, id) {
		t.Fatal("turn still owed after the resumed countdown elapsed")
	}
}

func TestAutoSettle_KeystrokeRacingTheFireHoldsInsteadOfSettling(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastAutoSettleActivityAt[id] = time.Now()
	d.lastInputMu.Unlock()

	var outcome string
	d.autoSettleFireHook = func(_, action string) { outcome = action }
	fireAutoSettleNow(t, d, id)

	if outcome != "held" {
		t.Fatalf("outcome = %q, want held", outcome)
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled by a countdown that fired into a keystroke")
	}
}

func TestAutoSettle_PointerMovementFreezesTheCountdownWithoutClaimingTyping(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	movePointerIn(d, id)

	held, ok := autoSettlePending(d, id)
	if !ok || held.phase != autoSettleHeld || held.resume != autoSettleCounting {
		t.Fatalf("after pointer movement: pending=%v entry=%+v, want held(resume=counting)", ok, held)
	}
	if d.userInputQuietRemaining(id, time.Hour) > 0 {
		t.Fatal("pointer movement was recorded as keyboard input; it would delay ticket nudges")
	}
	if !protocol.Deref(d.sessionForBroadcast(d.store.Get(id)).AutoSettleHeld) {
		t.Fatal("auto_settle_held absent after pointer movement")
	}

	movePointerIn(d, id)
	fireAutoSettleNow(t, d, id)
	if pending, ok := autoSettlePending(d, id); !ok || pending.phase != autoSettleHeld {
		t.Fatalf("fresh pointer movement did not extend the hold: pending=%v entry=%+v", ok, pending)
	}

	goQuiet(d, id)
	fireAutoSettleNow(t, d, id)
	if resumed, ok := autoSettlePending(d, id); !ok || resumed.phase != autoSettleCounting {
		t.Fatalf("after pointer quiet: pending=%v entry=%+v, want counting", ok, resumed)
	}
}

func TestAutoSettle_PointerMovementInsideTheSettleStillHoldsIt(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	moved := false
	d.autoSettlePreSettleHook = func() {
		if moved {
			return
		}
		moved = true
		movePointerIn(d, id)
	}
	var outcome string
	d.autoSettleFireHook = func(_, action string) { outcome = action }
	fireAutoSettleNow(t, d, id)

	if !moved || outcome != "held" {
		t.Fatalf("pointer race: moved=%v outcome=%q, want true/held", moved, outcome)
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled around pointer activity that landed before the settle committed")
	}
}

func TestAutoSettle_KeystrokeInsideTheSettleStillHoldsIt(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	typed := false
	d.autoSettlePreSettleHook = func() {
		if typed {
			return
		}
		typed = true
		typeInto(d, id)
	}
	var outcome string
	d.autoSettleFireHook = func(_, action string) { outcome = action }
	fireAutoSettleNow(t, d, id)

	if !typed {
		t.Fatal("the countdown never reached the settle; the test stood in the wrong gap")
	}
	if outcome != "held" {
		t.Fatalf("outcome = %q, want held", outcome)
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn settled around a keystroke that landed before the settle committed")
	}
	held, ok := autoSettlePending(d, id)
	if !ok || held.phase != autoSettleHeld || held.resume != autoSettleCounting {
		t.Fatalf("after the racing keystroke: pending=%v entry=%+v, want held(resume=counting)", ok, held)
	}
}

func TestAutoSettle_TypingDuringTheArmDelayHoldsItInvisibly(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}

	typeInto(d, id)

	held, ok := autoSettlePending(d, id)
	if !ok || held.phase != autoSettleHeld || held.resume != autoSettleArming {
		t.Fatalf("after typing during the arm delay: pending=%v entry=%+v, want held(resume=arming)", ok, held)
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleHeld != nil {
		t.Fatal("auto_settle_held rode the wire for a frozen arm delay; nothing was on screen to freeze")
	}
	if clone.AutoSettleFiresAt != nil {
		t.Fatalf("auto_settle_fires_at = %q during a held arm delay", *clone.AutoSettleFiresAt)
	}

	goQuiet(d, id)
	fireAutoSettleNow(t, d, id)

	resumed, ok := autoSettlePending(d, id)
	if !ok || resumed.phase != autoSettleArming {
		t.Fatalf("after the quiet window: pending=%v entry=%+v, want the arm delay back", ok, resumed)
	}
}

func TestAutoSettle_HoldExpiresWhereCancelStands(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)
	typeInto(d, id)

	if d.autoSettleDismissalArmed(id) {
		t.Fatal("typing armed a standing dismissal; a hold must expire on its own")
	}
	goQuiet(d, id)
	fireAutoSettleNow(t, d, id)
	if entry, ok := autoSettlePending(d, id); !ok || entry.phase != autoSettleCounting {
		t.Fatalf("countdown did not come back after the hold: pending=%v entry=%+v", ok, entry)
	}
}

func TestAutoSettle_LeavingWorkingClearsAHold(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)
	typeInto(d, id)

	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StatePendingApproval, cause: liveSignal{}}) {
		t.Fatal("applyState(pending_approval) = false")
	}

	if _, ok := autoSettlePending(d, id); ok {
		t.Fatal("a hold survived the session leaving working")
	}
	clone := d.sessionForBroadcast(d.store.Get(id))
	if clone.AutoSettleHeld != nil {
		t.Fatal("auto_settle_held survived the session leaving working")
	}
	if !turnIsOwed(d, id) {
		t.Fatal("turn was settled on the move to pending_approval")
	}
}

func TestAutoSettle_AutomationInputDoesNotHold(t *testing.T) {
	d, id := newAutoSettleDaemon(t)
	if !d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}}) {
		t.Fatal("applyState(working) = false")
	}
	fireAutoSettleNow(t, d, id)

	for _, source := range []string{"automation", "attach_replay"} {
		typeIntoAs(d, id, source)
		entry, ok := autoSettlePending(d, id)
		if !ok || entry.phase != autoSettleCounting {
			t.Fatalf("source %q: pending=%v entry=%+v, want the countdown still running", source, ok, entry)
		}
	}
}
