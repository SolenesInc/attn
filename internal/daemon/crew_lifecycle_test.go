package daemon

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"net"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type doorbellRecorder struct {
	mu       sync.Mutex
	writes   []string
	autoTake bool
}

func (r *doorbellRecorder) prompts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.writes))
	for _, write := range r.writes {
		if !strings.HasPrefix(write, sessionInputPasteStart) {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(write, sessionInputPasteStart), sessionInputPasteEnd))
	}
	return out
}

func (r *doorbellRecorder) setAutoTake(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoTake = enabled
}

func newLifecycleDaemon(t *testing.T) (*Daemon, string, *doorbellRecorder) {
	t.Helper()
	previous := sessionInputSubmitDelay
	sessionInputSubmitDelay = time.Millisecond
	t.Cleanup(func() { sessionInputSubmitDelay = previous })
	previousTakenWindow := sessionInputTakenWindow
	sessionInputTakenWindow = 100 * time.Millisecond
	t.Cleanup(func() { sessionInputTakenWindow = previousTakenWindow })

	d, backend, _ := newWakeableDaemon(t)
	recorder := &doorbellRecorder{autoTake: true}
	var submitted string
	var sessionID string
	backend.onInput = func(_ string, data []byte) {
		recorder.mu.Lock()
		recorder.writes = append(recorder.writes, string(data))
		if strings.HasPrefix(string(data), sessionInputPasteStart) {
			submitted = strings.TrimSuffix(strings.TrimPrefix(string(data), sessionInputPasteStart), sessionInputPasteEnd)
		}
		prompt := submitted
		autoTake := recorder.autoTake
		recorder.mu.Unlock()
		if autoTake && string(data) == "\r" && prompt != "" && sessionID != "" {
			go d.observePromptTaken(sessionID, prompt, time.Now())
		}
	}
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	sessionID = woken.SessionID
	d.evidenceTable().updateIf(woken.SessionID, nil, nil, func(e *sessionstate.Evidence) { e.InitialPromptOwed = false })
	return d, woken.SessionID, recorder
}

func setSessionActivity(t *testing.T, d *Daemon, sessionID string, state protocol.SessionState, at time.Time) {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("no session %s in the store", sessionID)
	}
	session.State = state
	session.StateSince = string(protocol.NewTimestamp(at))
	session.StateUpdatedAt = string(protocol.NewTimestamp(at))
	session.LastModelRequestAt = protocol.Ptr(string(protocol.NewTimestamp(at)))
	d.store.Remove(sessionID)
	d.store.Add(session)
	got := d.store.Get(sessionID)
	if got == nil || protocol.Deref(got.LastModelRequestAt) != protocol.Deref(session.LastModelRequestAt) {
		t.Fatalf("last model request fixture = %v, want %s", got, protocol.Deref(session.LastModelRequestAt))
	}
}

func crewMemberRecord(t *testing.T, d *Daemon, id string) crew.Member {
	t.Helper()
	members, _, err := d.readCrewMembers()
	if err != nil {
		t.Fatalf("read the roster: %v", err)
	}
	for _, member := range members {
		if member.ID == id {
			return member
		}
	}
	t.Fatalf("no member %q in the registry", id)
	return crew.Member{}
}

func setUserAway(d *Daemon, since time.Time) {
	d.presenceMu.Lock()
	defer d.presenceMu.Unlock()
	d.presentSince = since
}

func TestCrewLifecycleTick_IsSilentOnAQuietAttendedSession(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-10*time.Minute))

	for i := 0; i < 40; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a quiet attended session was sent %d prompts: %q", len(got), got)
	}
}

func TestCrewLifecycleTick_WarmsAContextThatIsAboutToLapse(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != crewHeartbeatPrompt {
		t.Fatalf("the tick sent %q, want one heartbeat", prompts)
	}

	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("the tick sent %d prompts a minute later; an unanswered nudge must not repeat every tick", len(got))
	}
}

func TestCrewLifecycleTick_UntakenHeartbeatLeavesClockAndReleasesTheLane(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	recorder.setAutoTake(false)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWaitingInput, now.Add(-58*time.Minute))
	requestAt := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)
	requestTime := protocol.Timestamp(requestAt).Time()

	d.crewLifecycleTick(now)
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("first attempt pasted %q, want one heartbeat", got)
	}
	if !d.crewMemo().heartbeatDue(sessionID, now.Add(time.Minute), d.crewHeartbeatLead()) {
		t.Fatal("an untaken heartbeat was charged as a cache warm")
	}
	if got := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt); got != requestAt {
		t.Fatalf("untaken heartbeat moved last_model_request_at from %s to %s", requestAt, got)
	}

	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("untaken retry repasted the heartbeat: %q", got)
	}
	if got := protocol.Deref(d.store.Get(sessionID).LastModelRequestAt); got != requestAt {
		t.Fatalf("untaken retry moved last_model_request_at from %s to %s", requestAt, got)
	}

	delivery, err := d.store.EnqueueMaintenancePrompt("after-untaken-heartbeat", sessionID, "durable follow-up", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.deliverAgentMailboxItem(delivery); err != nil {
		t.Fatalf("untaken heartbeat blocked a later inbox doorbell: %v", err)
	}
	if got := recorder.prompts(); len(got) != 2 || got[0] != crewHeartbeatPrompt || got[1] != agentMailboxDoorbellText {
		t.Fatalf("prompts after the untaken heartbeat = %q, want heartbeat then generic doorbell", got)
	}
	if got := protocol.Timestamp(protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)).Time(); !got.Equal(requestTime) {
		t.Fatalf("untaken heartbeat moved last_model_request_at from %s to %s", requestTime, got)
	}
}

func TestCrewLifecycleTick_AsksForTheHandoffWhenTheUserIsGone(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))
	setUserAway(d, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != agentMailboxDoorbellText {
		t.Fatalf("the tick sent %q, want the handoff ask", prompts)
	}
	d.crewLifecycleTick(now.Add(2 * time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("the handoff was asked for %d times inside the grace", len(got))
	}
}

func TestCrewLifecycleTick_CanAskAgainAfterTheInboxReadAndGrace(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))
	setUserAway(d, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)
	first, remaining, err := d.store.ReadAgentMailbox(sessionID, 1, now.Add(time.Minute))
	if err != nil || len(first) != 1 || remaining != 0 {
		t.Fatalf("first auto-sleep inbox read = %+v, remaining=%d err=%v", first, remaining, err)
	}
	d.noteAgentMailboxRead(sessionID, remaining)

	retryAt := now.Add(d.crewCacheTTL(string(d.store.Get(sessionID).Agent)))
	d.crewLifecycleTick(retryAt)
	second, err := d.store.UnreadAgentMailboxDeliveries(sessionID)
	if err != nil || len(second) != 1 {
		t.Fatalf("second auto-sleep inbox = %+v, %v", second, err)
	}
	if second[0].Item.ID == first[0].Item.ID {
		t.Fatalf("auto-sleep reused read mailbox id %q", second[0].Item.ID)
	}
	if got := recorder.prompts(); len(got) != 2 || got[0] != agentMailboxDoorbellText || got[1] != agentMailboxDoorbellText {
		t.Fatalf("auto-sleep prompts across the read = %q, want two generic doorbells", got)
	}
}

func TestCrewLifecycleTick_LeavesAnUnreachableSessionAlone(t *testing.T) {
	for _, state := range []protocol.SessionState{
		protocol.SessionStatePendingApproval,
		protocol.SessionStateWorking,
	} {
		t.Run(string(state), func(t *testing.T) {
			d, sessionID, recorder := newLifecycleDaemon(t)
			now := time.Now()
			setSessionActivity(t, d, sessionID, state, now.Add(-2*time.Hour))

			d.crewLifecycleTick(now)
			setUserAway(d, now.Add(-3*time.Hour))
			d.crewLifecycleTick(now.Add(time.Minute))

			if got := recorder.prompts(); len(got) != 0 {
				t.Fatalf("a session in %s was sent %q", state, got)
			}
		})
	}
}

func TestCrewLifecycleTick_WarmsAWaitingDaySafely(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWaitingInput, now.Add(-58*time.Minute))

	d.crewLifecycleTick(now)
	if got := recorder.prompts(); len(got) != 1 || got[0] != crewHeartbeatPrompt {
		t.Fatalf("a waiting member was sent %q, want one heartbeat", got)
	}

	setUserAway(d, now.Add(-3*time.Hour))
	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("a successful heartbeat was followed immediately by %q, want the warmed cache left alone", got)
	}
	d.crewLifecycleTick(now.Add(58 * time.Minute))
	prompts := recorder.prompts()
	if len(prompts) != 2 || prompts[1] != agentMailboxDoorbellText {
		t.Fatalf("the tick sent %q, want the handoff ask", prompts)
	}
}

func TestCrewLifecycleTick_HonoursItsSwitches(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))

	d.store.SetSetting(SettingCrewHeartbeatEnabled, "false")
	d.crewLifecycleTick(now)
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("heartbeats are off and the tick sent %q", got)
	}

	d.store.SetSetting(SettingCrewAutoSleepEnabled, "false")
	setUserAway(d, now.Add(-3*time.Hour))
	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("auto-sleep is off and the tick sent %q", got)
	}
}

func TestCrewCacheTTL_TakesThePerAgentAssumptionAndItsOverride(t *testing.T) {
	d := newCrewDaemon(t)
	if got := d.crewCacheTTL("codex"); got != crewCacheTTLCodex*time.Second {
		t.Fatalf("codex TTL = %s, want %ds", got, crewCacheTTLCodex)
	}
	if got := d.crewCacheTTL(""); got != crewCacheTTLDefault*time.Second {
		t.Fatalf("unnamed TTL = %s, want %ds", got, crewCacheTTLDefault)
	}
	d.store.SetSetting(SettingCrewCacheTTLPrefix+"codex", "600")
	if got := d.crewCacheTTL("codex"); got != 10*time.Minute {
		t.Fatalf("overridden codex TTL = %s, want 10m", got)
	}
	d.store.SetSetting(SettingCrewCacheTTLPrefix+"codex", "not-a-number")
	if got := d.crewCacheTTL("codex"); got != crewCacheTTLCodex*time.Second {
		t.Fatalf("a bad override gave %s, want the %ds assumption back", got, crewCacheTTLCodex)
	}
}

func TestChargeAutonomousWake_ForgetsWakesOlderThanTheWindow(t *testing.T) {
	d := newCrewDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "1")
	d.store.SetSetting(SettingCrewWakeLimitWindowSeconds, "3600")
	now := time.Now()

	if err := d.chargeAutonomousWake("trellis", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("first wake: %v", err)
	}
	if err := d.chargeAutonomousWake("trellis", now); err != nil {
		t.Fatalf("a wake was refused on last night's allowance: %v", err)
	}
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 1 {
		t.Fatalf("the ledger kept %d stamps, want only the one inside the window", got)
	}
}

func TestCrewHandoff_AWakeLimitRefusalLeavesTheDayRunningAndSaysWhy(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "0")
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "the tests are green\n", Close: protocol.Ptr(protocol.CrewDayCloseNap),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	napErr := protocol.Deref(resp.CrewHandoffResult.NapError)
	if !strings.Contains(napErr, "crew.wake_limit=0") {
		t.Fatalf("the nap failed with %q, which does not name the limit that stopped it", napErr)
	}
	if got := spawnedSessions(t, backend); len(got) != 1 {
		t.Fatalf("%d sessions were spawned; a refused wake must spawn nothing", len(got))
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("the day was closed behind a wake that never happened")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != woken.SessionID {
		t.Fatalf("binding = %q, want the day that is still running %q", got, woken.SessionID)
	}
}

func TestCrewHandoff_EndsTheDayWhenTheUserHasBeenAway(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	resp := crewHandoffCall(t, d, woken.SessionID, "nothing is in flight\n")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseSleep {
		t.Fatalf("outcome = %q, want sleep", got)
	}
	if protocol.Deref(result.SessionID) != "" {
		t.Fatalf("a successor %q was woken for a user who is not there", protocol.Deref(result.SessionID))
	}
	if got := spawnedSessions(t, backend); len(got) != 1 {
		t.Fatalf("%d sessions were spawned, want only the original wake", len(got))
	}
	if d.store.Get(woken.SessionID) != nil {
		t.Fatal("the day that filed its letter is still running")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "" {
		t.Fatalf("the member is still bound to %q after going to sleep", got)
	}
	if names := handoffFiles(t, d, "trellis"); len(names) != 2 {
		t.Fatalf("the handoffs dir holds %v, want the seeded letter and this one", names)
	}
}

func TestCrewHandoff_NapOverridesTheAbsence(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "picked up #901\n", Close: protocol.Ptr(protocol.CrewDayCloseNap),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if napErr := protocol.Deref(result.NapError); napErr != "" {
		t.Fatalf("the nap did not run: %s", napErr)
	}
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseNap {
		t.Fatalf("outcome = %q, want nap", got)
	}
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 1 {
		t.Fatalf("an unattended turnover booked %d wakes, want 1", got)
	}
}

func TestCrewLifecycleTick_SleepAskHeldOffByTypingLandsAfterTheQuietWindow(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	d.store.SetSetting(SettingCrewHeartbeatEnabled, "false")
	quiesceTranscriptWatchers(t, d)
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))
		setUserAway(d, now.Add(-3*time.Hour))
		if err := d.writeSessionPTY(sessionID, []byte("half written"), "user"); err != nil {
			t.Fatalf("user input: %v", err)
		}

		d.crewLifecycleTick(now)
		if got := recorder.prompts(); len(got) != 0 {
			t.Fatalf("typed into a composer the user just used: %q", got)
		}
		d.crewLifecycleTick(now.Add(time.Minute))
		if got := recorder.prompts(); len(got) != 0 {
			t.Fatalf("the grace was not spent, so this witness proves nothing: %q", got)
		}

		time.Sleep(sessionInputQuietWindow)
		settleResend(t)
		if got := recorder.prompts(); len(got) != 1 || got[0] != agentMailboxDoorbellText {
			t.Fatalf("prompts after the window = %q, want one sleep ask", got)
		}
	})
}
