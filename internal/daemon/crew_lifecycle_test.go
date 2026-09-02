package daemon

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"net"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

type doorbellRecorder struct {
	mu       sync.Mutex
	writes   []string
	autoTake bool
	taken    chan struct{}
}

func TestCrewSleepPrompt_ForcesThePromisedSleep(t *testing.T) {
	for _, want := range []string{
		"`attn handoff --sleep",
		"nobody wakes behind it",
		"not be woken again until the user asks",
	} {
		if !strings.Contains(crewSleepPrompt, want) {
			t.Errorf("sleep prompt does not carry %q", want)
		}
	}
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

func (r *doorbellRecorder) expectTake() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taken = make(chan struct{})
	return r.taken
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
		var taken chan struct{}
		if autoTake && string(data) == "\r" && prompt != "" && sessionID != "" {
			taken = recorder.taken
			recorder.taken = nil
		}
		recorder.mu.Unlock()
		if autoTake && string(data) == "\r" && prompt != "" && sessionID != "" {
			go func() {
				d.observePromptTaken(sessionID, prompt, time.Now())
				if taken != nil {
					close(taken)
				}
			}()
		}
	}
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	sessionID = woken.SessionID
	d.agentMailboxMu.Lock()
	delete(d.postInitialPrompt, woken.SessionID)
	d.agentMailboxMu.Unlock()
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

func TestCrewLifecycleTick_UntakenHeartbeatLeavesClockAndRetryDoesNotRepaste(t *testing.T) {
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

	recorder.setAutoTake(true)
	d.crewLifecycleTick(now.Add(2 * time.Minute))
	if d.crewMemo().heartbeatDue(sessionID, now.Add(3*time.Minute), d.crewHeartbeatLead()) {
		t.Fatal("a positively taken heartbeat did not start its grace")
	}
	if got := protocol.Timestamp(protocol.Deref(d.store.Get(sessionID).LastModelRequestAt)).Time(); !got.After(requestTime) {
		t.Fatalf("taken heartbeat left last_model_request_at at %s, want after %s", got, requestTime)
	}
}

func TestCrewLifecycleTick_AsksForTheHandoffWhenTheUserIsGone(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))
	setUserAway(d, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != crewSleepPrompt {
		t.Fatalf("the tick sent %q, want the handoff ask", prompts)
	}
	d.crewLifecycleTick(now.Add(2 * time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("the handoff was asked for %d times inside the grace", len(got))
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
	if len(prompts) != 2 || prompts[1] != crewSleepPrompt {
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

func TestCrewCacheState_TreatsAWorkingSessionAsMidRequest(t *testing.T) {
	d, sessionID, _ := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-2*time.Hour))

	state := d.crewCacheState(d.store.Get(sessionID), now)
	if state.Age != 0 {
		t.Fatalf("a working session's cache reads %s old, want 0", state.Age)
	}
	if state.TTL != crewCacheTTLClaude*time.Second {
		t.Fatalf("cache TTL = %s, want claude's assumed %ds", state.TTL, crewCacheTTLClaude)
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

func TestChargeAutonomousWake_BooksWakesAndRefusesPastTheLimit(t *testing.T) {
	d := newCrewDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "2")
	now := time.Now()

	for i := 0; i < 2; i++ {
		if err := d.chargeAutonomousWake("trellis", now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("wake %d was refused: %v", i+1, err)
		}
	}
	member := crewMemberRecord(t, d, "trellis")
	if got := len(member.AutonomousWakes); got != 2 {
		t.Fatalf("the roster records %d autonomous wakes, want 2", got)
	}

	err := d.chargeAutonomousWake("trellis", now.Add(2*time.Minute))
	if err == nil {
		t.Fatal("a third wake was allowed past a limit of 2")
	}
	for _, want := range []string{"Trellis", "crew.wake_limit=2", "nothing was woken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 2 {
		t.Fatalf("a refused wake was charged anyway: %d stamps", got)
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

func TestCrewHandoff_SleepIsExplicitEvenWithTheUserHere(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "signing off for the night\n", Close: protocol.Ptr(protocol.CrewDayCloseSleep),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	if got := protocol.Deref(resp.CrewHandoffResult.Outcome); got != protocol.CrewDayCloseSleep {
		t.Fatalf("outcome = %q, want sleep", got)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "" {
		t.Fatalf("the member is still bound to %q after being told to sleep", got)
	}
}

func TestCrewHandoff_TeardownPreparationFailureKeepsTheDayRunning(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	d.prepareSessionTeardownHook = func(string) error { return errors.New("tombstone write failed") }
	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "cannot close yet\n", Close: protocol.Ptr(protocol.CrewDayCloseSleep),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if resp.Ok {
		t.Fatal("handoff reported success after teardown preparation failed")
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("failed handoff removed the running day")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != woken.SessionID {
		t.Fatalf("binding = %q, want original day %q", got, woken.SessionID)
	}
	if got := spawnedSessions(t, backend); len(got) != 1 {
		t.Fatalf("spawned sessions = %v, want no successor", got)
	}
}

func TestCrewLifecycleMemo_ForgetsAClosedSession(t *testing.T) {
	memo := newCrewLifecycleMemo()
	now := time.Now()
	if !memo.heartbeatDue("a", now, time.Hour) {
		t.Fatal("the first heartbeat was refused")
	}
	if !memo.heartbeatDue("a", now.Add(time.Minute), time.Hour) {
		t.Fatal("an unconfirmed heartbeat was charged against the grace")
	}
	memo.recordHeartbeat("a", now)
	if memo.heartbeatDue("a", now.Add(time.Minute), time.Hour) {
		t.Fatal("a second heartbeat slipped through the grace")
	}
	if !memo.mayAsk("a", now, time.Hour) {
		t.Fatal("a heartbeat's grace blocked the handoff ask; they are separate acts")
	}
	memo.forget("a")
	if !memo.heartbeatDue("a", now.Add(time.Minute), time.Hour) {
		t.Fatal("a forgotten session is still holding its grace")
	}
}

func setSessionContextOccupancy(t *testing.T, d *Daemon, sessionID string, tokens, window int64) {
	t.Helper()
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	if watcher == nil {
		watcher = newTranscriptWatcher(sessionID, protocol.SessionAgentClaude, "", time.Now(), nil)
		if d.transcriptWatch == nil {
			d.transcriptWatch = make(map[string]*transcriptWatcher)
		}
		d.transcriptWatch[sessionID] = watcher
	}
	d.watchersMu.Unlock()
	watcher.observeOccupancy(transcript.ContextObservation{Tokens: tokens, Window: window})
}

func TestCrewLifecycleTick_AsksForTheHandoffWhenTheContextIsFull(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 {
		t.Fatalf("the tick sent %q, want the context handoff ask", prompts)
	}
	for _, want := range []string{
		"160000 of the 160000 tokens",
		"`attn handoff --nap -m",
		"Write the letter first",
		"carry on without asking",
	} {
		if !strings.Contains(prompts[0], want) {
			t.Fatalf("the context handoff ask does not carry %q: %q", want, prompts[0])
		}
	}
}

func TestCrewLifecycleTick_AsksAboutAFullContextExactlyOnce(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault+50000, 0)

	for i := 0; i < 30; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("a full context was asked about %d times over half an hour", len(got))
	}
}

func TestCrewLifecycleTick_ReArmsAfterAContextThatCameBackUnderBudget(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))

	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	firstTaken := recorder.expectTake()
	d.crewLifecycleTick(now)
	<-firstTaken
	setSessionContextOccupancy(t, d, sessionID, 20000, 0)
	d.crewLifecycleTick(now.Add(time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	secondTaken := recorder.expectTake()
	d.crewLifecycleTick(now.Add(2 * time.Minute))
	<-secondTaken

	if got := recorder.prompts(); len(got) != 2 {
		t.Fatalf("the tick sent %d asks across two separate fills: %q", len(got), got)
	}
}

func TestCrewLifecycleTick_SaysNothingWithoutAReading(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))

	for i := 0; i < 10; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a session with no occupancy reading was sent %q", got)
	}
}

// Measured over the corpus behind this feature: 7 of 286 auto-compactions finished their
// whole climb inside one turn, the worst burning 159,674 tokens without going idle.
func TestCrewLifecycleTick_AsksAMemberThatIsStillWorking(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("a working member with a full context was sent %q, want the handoff ask", got)
	}
}

func TestCrewLifecycleTick_LeavesAWorkingMemberAloneWithoutContextPressure(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a working member with room left was sent %q", got)
	}
}

func TestCrewLifecycleTick_ContextHandoffCanBeTurnedOff(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	d.store.SetSetting(SettingCrewContextHandoffEnabled, "false")

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("the context half was off and the tick still sent %q", got)
	}
}

func TestCrewContextBudget(t *testing.T) {
	d, sessionID, _ := newLifecycleDaemon(t)
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("no session %s", sessionID)
	}

	t.Run("the shipped default with no window in sight", func(t *testing.T) {
		setSessionContextOccupancy(t, d, sessionID, 1000, 0)
		if got := d.crewContextPressure(session); got.Budget != crewContextBudgetDefault {
			t.Fatalf("budget = %d, want %d", got.Budget, crewContextBudgetDefault)
		}
	})

	t.Run("a stated window smaller than the budget lowers it, minus the letter's room", func(t *testing.T) {
		setSessionContextOccupancy(t, d, sessionID, 1000, 100000)
		want := int64(100000 - crewContextHandoffMargin)
		if got := d.crewContextPressure(session); got.Budget != want {
			t.Fatalf("budget = %d, want %d", got.Budget, want)
		}
	})

	t.Run("a window bigger than the budget does not raise it", func(t *testing.T) {
		setSessionContextOccupancy(t, d, sessionID, 1000, 1000000)
		if got := d.crewContextPressure(session); got.Budget != crewContextBudgetDefault {
			t.Fatalf("budget = %d, want the setting to stay the ceiling", got.Budget)
		}
	})

	t.Run("the setting moves it", func(t *testing.T) {
		d.store.SetSetting(SettingCrewContextHandoffTokens, "90000")
		t.Cleanup(func() { d.store.SetSetting(SettingCrewContextHandoffTokens, "") })
		setSessionContextOccupancy(t, d, sessionID, 1000, 0)
		if got := d.crewContextPressure(session); got.Budget != 90000 {
			t.Fatalf("budget = %d, want the configured 90000", got.Budget)
		}
	})

	t.Run("no reading is no pressure", func(t *testing.T) {
		other := *session
		other.ID = "no-watcher-session"
		if got := d.crewContextPressure(&other); got.Tokens != 0 || got.Budget != 0 {
			t.Fatalf("pressure = %+v, want nothing at all", got)
		}
	})
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
		if got := recorder.prompts(); len(got) != 1 || got[0] != crewSleepPrompt {
			t.Fatalf("prompts after the window = %q, want one sleep ask", got)
		}
	})
}
