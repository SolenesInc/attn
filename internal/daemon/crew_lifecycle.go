package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	crewLifecycleKind     = "crew_lifecycle_tick"
	crewLifecycleInterval = 60 * time.Second
	crewLifecycleTimeout  = 60 * time.Second
)

// Assumed prompt-cache lifetimes, per vendor policy — no API reports one. An unnamed
// harness gets the hour: too short costs ~$1.80/h in heartbeats, too long one ~$3.00.
const (
	crewCacheTTLClaude  = 3600
	crewCacheTTLCodex   = 1800
	crewCacheTTLDefault = 3600
)

// Receipt, measured 2026-08-19 over 265 real Claude Code auto-compactions: the lowest
// compaction was 185,578 tokens and the worst handoff letter 16,916.
const (
	crewContextBudgetDefault   = 160000
	crewContextHandoffMargin   = 25000
	crewContextBudgetMinTokens = 10000
	crewContextBudgetMaxTokens = 2000000
)

const crewHeartbeatLeadDefault = 300

// Receipt, measured 2026-08-14 over 12.4 days of the production event log: gaps between
// user-caused facts run continuously to 7,556s and then jump to 18,468s.
const crewAwayDefault = 9000

const (
	crewWakeLimitDefault        = 8
	crewWakeLimitWindowDefault  = 43200
	crewWakeLimitMax            = 1000
	crewWakeLimitWindowMinSecs  = 60
	crewWakeLimitWindowMaxSecs  = 7 * 24 * 3600
	crewCacheTTLMinSeconds      = 60
	crewCacheTTLMaxSeconds      = 24 * 3600
	crewHeartbeatLeadMinSeconds = 30
	crewHeartbeatLeadMaxSeconds = 3600
	crewAwayMinSeconds          = 60
	crewAwayMaxSeconds          = 24 * 3600
)

const crewHeartbeatPrompt = "[attn] This is a cache heartbeat. Ignore it as user input, do no work, and repeat your previous message verbatim for the user."

const crewSleepPrompt = "[attn] The user has been away long enough that your day should end rather than carry on warm. Close it now: write your letter to whoever wakes as you next — what you were doing, what is load-bearing, what you would pick up first — and file it with `attn handoff --sleep -m \"<your letter>\"`. Your session ends when it lands; nobody wakes behind it, and you will not be woken again until the user asks."

func crewContextHandoffPrompt(tokens, budget int64) string {
	return fmt.Sprintf("[attn] Your context is at %d of the %d tokens your day gets, and past that your harness compacts it — which would leave its summary of today where your letter should be. This is a day cut short, not a day finished, so close it yourself now. Write the letter first; it is the part that cannot be recovered, and write it so whoever wakes as you next can carry on without asking: what you were in the middle of, exactly where it stands, what is load-bearing, and the first concrete thing they should do. Then file it with `attn handoff --nap -m \"<your letter>\"` — `--nap` wakes your successor even if the user is away, which is right here because the work did not end, you ran out of room. Use plain `attn handoff` instead only if you were genuinely finished and there is nothing to carry.", tokens, budget)
}

const crewSleepPromptGrace = 10 * time.Minute

type crewLifecycleMemo struct {
	mu       sync.Mutex
	acted    map[string]time.Time
	asked    map[string]time.Time
	full     map[string]bool
	episodes map[string]uint64
}

func newCrewLifecycleMemo() *crewLifecycleMemo {
	return &crewLifecycleMemo{
		acted:    make(map[string]time.Time),
		asked:    make(map[string]time.Time),
		full:     make(map[string]bool),
		episodes: make(map[string]uint64),
	}
}

func (m *crewLifecycleMemo) heartbeatDue(sessionID string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.acted[sessionID]
	return !ok || now.Sub(last) >= grace
}

func (m *crewLifecycleMemo) recordHeartbeat(sessionID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acted[sessionID] = at
}

func (m *crewLifecycleMemo) mayAsk(sessionID string, now time.Time, grace time.Duration) bool {
	return m.mayAct(m.asked, sessionID, now, grace)
}

func (m *crewLifecycleMemo) mayAct(table map[string]time.Time, sessionID string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := table[sessionID]; ok && now.Sub(last) < grace {
		return false
	}
	table[sessionID] = now
	return true
}

func (m *crewLifecycleMemo) mayAskContextFull(sessionID string) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.full[sessionID] {
		return 0, false
	}
	m.full[sessionID] = true
	m.episodes[sessionID]++
	return m.episodes[sessionID], true
}

func (m *crewLifecycleMemo) rearmContextFull(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.full, sessionID)
}

func (m *crewLifecycleMemo) forget(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.acted, sessionID)
	delete(m.asked, sessionID)
	delete(m.full, sessionID)
	delete(m.episodes, sessionID)
}

func (d *Daemon) crewBoolSetting(name string) bool {
	if d.store == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(d.store.GetSetting(name)), "false")
}

func (d *Daemon) crewSeconds(name string, fallback, min, max int) time.Duration {
	seconds := fallback
	if d.store != nil {
		raw := strings.TrimSpace(d.store.GetSetting(name))
		if raw != "" {
			parsed := resolveBoundedIntSetting(raw, fallback, min, max)
			if parsed == fallback && raw != fmt.Sprint(fallback) {
				d.logf("crew: %s is %q, which is not a whole number of seconds between %d and %d; using %d", name, raw, min, max, fallback)
			}
			seconds = parsed
		}
	}
	return time.Duration(seconds) * time.Second
}

func (d *Daemon) crewCacheTTL(agent string) time.Duration {
	agent = strings.ToLower(strings.TrimSpace(agent))
	fallback := crewCacheTTLDefault
	switch agent {
	case "claude":
		fallback = crewCacheTTLClaude
	case "codex":
		fallback = crewCacheTTLCodex
	}
	if agent != "" && d.store != nil {
		if raw := strings.TrimSpace(d.store.GetSetting(SettingCrewCacheTTLPrefix + agent)); raw != "" {
			return d.crewSeconds(SettingCrewCacheTTLPrefix+agent, fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
		}
	}
	return d.crewSeconds(SettingCrewCacheTTLSeconds, fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
}

func (d *Daemon) crewContextPressure(session *protocol.Session) crew.ContextPressure {
	occupancy, ok := d.sessionContextOccupancy(session.ID)
	if !ok {
		return crew.ContextPressure{}
	}
	budget := d.crewContextBudget()
	if window := d.crewContextWindow(session, occupancy); window > 0 && window-crewContextHandoffMargin < budget {
		budget = window - crewContextHandoffMargin
	}
	if budget < 1 {
		return crew.ContextPressure{}
	}
	return crew.ContextPressure{Tokens: occupancy.Tokens, Budget: budget}
}

func (d *Daemon) crewContextWindow(session *protocol.Session, occupancy transcript.ContextObservation) int64 {
	if cap := d.launchContextWindowCap(session.ID, string(session.Agent), d.isChiefOfStaffSession(session.ID)); cap > 0 {
		return int64(cap)
	}
	return occupancy.Window
}

func (d *Daemon) crewContextBudget() int64 {
	tokens := crewContextBudgetDefault
	if d.store != nil {
		if raw := strings.TrimSpace(d.store.GetSetting(SettingCrewContextHandoffTokens)); raw != "" {
			tokens = resolveBoundedIntSetting(raw, crewContextBudgetDefault, crewContextBudgetMinTokens, crewContextBudgetMaxTokens)
			if tokens == crewContextBudgetDefault && raw != fmt.Sprint(crewContextBudgetDefault) {
				d.logf("crew: %s is %q, which is not a whole number of tokens between %d and %d; using %d",
					SettingCrewContextHandoffTokens, raw, crewContextBudgetMinTokens, crewContextBudgetMaxTokens, crewContextBudgetDefault)
			}
		}
	}
	return int64(tokens)
}

func (d *Daemon) crewHeartbeatLead() time.Duration {
	return d.crewSeconds(SettingCrewHeartbeatLeadSeconds, crewHeartbeatLeadDefault, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
}

func (d *Daemon) crewAwayLimit() time.Duration {
	return d.crewSeconds(SettingCrewAwaySeconds, crewAwayDefault, crewAwayMinSeconds, crewAwayMaxSeconds)
}

func (d *Daemon) crewWakeLedger() crew.WakeLedger {
	limit := crewWakeLimitDefault
	if d.store != nil {
		if raw := strings.TrimSpace(d.store.GetSetting(SettingCrewWakeLimit)); raw != "" {
			limit = resolveBoundedIntSetting(raw, crewWakeLimitDefault, 0, crewWakeLimitMax)
		}
	}
	return crew.WakeLedger{
		Limit:  limit,
		Window: d.crewSeconds(SettingCrewWakeLimitWindowSeconds, crewWakeLimitWindowDefault, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs),
	}
}

func (d *Daemon) crewCacheState(session *protocol.Session, now time.Time) crew.CacheState {
	state := crew.CacheState{TTL: d.crewCacheTTL(string(session.Agent))}
	switch session.State {
	case protocol.SessionStateWorking, protocol.SessionStateLaunching:
		return state
	}
	updated := protocol.Timestamp(protocol.Deref(session.LastModelRequestAt)).Time()
	if updated.IsZero() || !now.After(updated) {
		return state
	}
	state.Age = now.Sub(updated)
	return state
}

func crewSessionReachable(session *protocol.Session) bool {
	return sessionInputPhaseAllows(sessionInputAtTurnBoundary, session.State)
}

func crewSessionMidTurn(session *protocol.Session) bool {
	return session.State == protocol.SessionStateWorking
}

func (d *Daemon) registerCrewLifecycleCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		crewLifecycleKind,
		crewLifecycleInterval,
		d.crewLifecycleHandler,
		jobs.HandlerConfig{Timeout: crewLifecycleTimeout},
	); err != nil {
		d.logf("crew: register lifecycle tick: %v", err)
	}
}

func (d *Daemon) crewLifecycleHandler(_ context.Context, _ *jobs.Job) (any, error) {
	d.crewLifecycleTick(time.Now())
	return nil, nil
}

func (d *Daemon) crewLifecycleTick(now time.Time) {
	if d.store == nil {
		return
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for the lifecycle tick: %v", err)
		}
		return
	}
	awayFor := d.UserAwayFor(now)
	awayLimit := d.crewAwayLimit()
	lead := d.crewHeartbeatLead()
	heartbeat := d.crewBoolSetting(SettingCrewHeartbeatEnabled)
	autoSleep := d.crewBoolSetting(SettingCrewAutoSleepEnabled)
	contextHandoff := d.crewBoolSetting(SettingCrewContextHandoffEnabled)
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		session := d.store.Get(member.BindingSession)
		if session == nil {
			continue
		}
		cache := d.crewCacheState(session, now)
		pressure := d.crewContextPressure(session)
		action := crew.Decide(crew.Signals{
			AwayFor:   awayFor,
			AwayLimit: awayLimit,
			Cache:     cache,
			Lead:      lead,
			Reachable: crewSessionReachable(session),
			MidTurn:   crewSessionMidTurn(session),
			Context:   pressure,

			HeartbeatEnabled:      heartbeat,
			AutoSleepEnabled:      autoSleep,
			ContextHandoffEnabled: contextHandoff,
		})
		if !pressure.Full() {
			d.crewMemo().rearmContextFull(session.ID)
		}
		contextEpisode := uint64(0)
		if action == crew.ActionContextHandoff {
			var allowed bool
			contextEpisode, allowed = d.crewMemo().mayAskContextFull(session.ID)
			if !allowed {
				continue
			}
		}
		if action == crew.ActionNone {
			continue
		}
		d.actOnCrewMember(member, session.ID, action, cache, pressure, contextEpisode, now)
	}
}

func (d *Daemon) actOnCrewMember(member crew.Member, sessionID string, action crew.Action, cache crew.CacheState, pressure crew.ContextPressure, contextEpisode uint64, now time.Time) {
	switch action {
	case crew.ActionHeartbeat:
		if !d.crewMemo().heartbeatDue(sessionID, now, d.crewHeartbeatLead()) {
			return
		}
		session := d.store.Get(sessionID)
		if session == nil {
			return
		}
		generation := protocol.Deref(session.LastModelRequestAt)
		id := inputAttemptID("crew-heartbeat", sessionID+"/"+generation)
		delivery := maintenanceSessionInput("crew-heartbeat", sessionID+"/"+generation, sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		attempt := d.sessionInputs().try(context.Background(), delivery)
		if attempt.err != nil {
			d.logf("crew: %s's heartbeat did not reach session %s: %v", crew.DisplayName(member.ID), sessionID, attempt.err)
			return
		}
		if attempt.stage != sessionInputTaken && sessionInputTakenWindow > 0 {
			attempt = d.sessionInputs().await(sessionID, id, attempt.wait, sessionInputTakenWindow)
		}
		if attempt.stage != sessionInputTaken {
			d.logf("crew: %s's heartbeat was placed in session %s but no model request took it", crew.DisplayName(member.ID), sessionID)
			return
		}
		d.crewMemo().recordHeartbeat(sessionID, attempt.receipt.takenAt)
		d.sessionInputs().release(sessionID, id)
		d.logf("crew: warmed %s's context in session %s (cache estimated %s old against a %s assumption)",
			crew.DisplayName(member.ID), sessionID, cache.Age.Round(time.Second), cache.TTL)
	case crew.ActionSleep:
		if !d.crewMemo().mayAsk(sessionID, now, crewSleepPromptGrace) {
			return
		}
		delivery := maintenanceSessionInput("crew-sleep", sessionID, sessionID, crewSleepPrompt, sessionInputAtTurnBoundary)
		attempt := d.sessionInputs().try(context.Background(), delivery)
		if attempt.err != nil {
			d.logf("crew: %s was not asked to close its day: %v", crew.DisplayName(member.ID), attempt.err)
			return
		}
		d.sessionInputs().release(sessionID, delivery.id)
		d.logf("crew: asked %s to close its day — the user has been away and the cache is %s from lapsing",
			crew.DisplayName(member.ID), cache.Remaining().Round(time.Second))
	case crew.ActionContextHandoff:
		key := fmt.Sprintf("%s/%d", sessionID, contextEpisode)
		delivery := maintenanceSessionInput("crew-context-handoff", key, sessionID, crewContextHandoffPrompt(pressure.Tokens, pressure.Budget), sessionInputAtTurnBoundary)
		attempt := d.sessionInputs().try(context.Background(), delivery)
		if attempt.err != nil {
			d.logf("crew: %s was not asked to hand off a full context: %v", crew.DisplayName(member.ID), attempt.err)
			return
		}
		d.sessionInputs().release(sessionID, delivery.id)
		d.logf("crew: asked %s to hand off — its context is at %d of the %d tokens its day gets",
			crew.DisplayName(member.ID), pressure.Tokens, pressure.Budget)
	}
}

func (d *Daemon) crewMemo() *crewLifecycleMemo {
	d.crewMemoOnce.Do(func() { d.crewLifecycleState = newCrewLifecycleMemo() })
	return d.crewLifecycleState
}

func (d *Daemon) chargeAutonomousWake(memberID string, now time.Time) error {
	ledger := d.crewWakeLedger()
	var refusal error
	if _, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
		kept, err := ledger.Allows(member.ID, now)
		if err != nil {
			refusal = err
			return false, nil
		}
		member.AutonomousWakes = formatWakeStamps(kept)
		return true, nil
	}); err != nil {
		return err
	}
	if refusal != nil {
		d.logf("crew: %v", refusal)
	}
	return refusal
}

func parseWakeStamps(raw []string) []time.Time {
	stamps := make([]time.Time, 0, len(raw))
	for _, value := range raw {
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		stamps = append(stamps, at)
	}
	return stamps
}

func formatWakeStamps(stamps []time.Time) []string {
	out := make([]string, 0, len(stamps))
	for _, at := range stamps {
		out = append(out, at.UTC().Format(time.RFC3339))
	}
	return out
}
