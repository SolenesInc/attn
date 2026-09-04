package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
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

var crewHeartbeatPrompt = prompts.RenderText("crew", "heartbeat", prompts.Values{})

var crewSleepPrompt = prompts.RenderText("crew", "sleep-away", prompts.Values{})

const crewSleepPromptGrace = 10 * time.Minute

type crewLifecycleMemo struct {
	mu                     sync.Mutex
	lastHeartbeat          map[string]time.Time
	lastSleepPromptAttempt map[string]time.Time
}

func newCrewLifecycleMemo() *crewLifecycleMemo {
	return &crewLifecycleMemo{
		lastHeartbeat:          make(map[string]time.Time),
		lastSleepPromptAttempt: make(map[string]time.Time),
	}
}

func (m *crewLifecycleMemo) heartbeatDue(sessionID string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastHeartbeat[sessionID]
	return !ok || now.Sub(last) >= grace
}

func (m *crewLifecycleMemo) recordHeartbeat(sessionID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHeartbeat[sessionID] = at
}

func (m *crewLifecycleMemo) mayPromptSleep(sessionID string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.lastSleepPromptAttempt[sessionID]; ok && now.Sub(last) < grace {
		return false
	}
	m.lastSleepPromptAttempt[sessionID] = now
	return true
}

func (m *crewLifecycleMemo) forget(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.lastHeartbeat, sessionID)
	delete(m.lastSleepPromptAttempt, sessionID)
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
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		session := d.store.Get(member.BindingSession)
		if session == nil {
			continue
		}
		cache := d.crewCacheState(session, now)
		action := crew.Decide(crew.Signals{
			AwayFor:          awayFor,
			AwayLimit:        awayLimit,
			Cache:            cache,
			Lead:             lead,
			Reachable:        crewSessionReachable(session),
			MidTurn:          crewSessionMidTurn(session),
			HeartbeatEnabled: heartbeat,
			AutoSleepEnabled: autoSleep,
		})
		switch action {
		case crew.ActionHeartbeat:
			if !d.crewMemo().heartbeatDue(session.ID, now, lead) {
				continue
			}
		case crew.ActionSleep:
			if !d.crewMemo().mayPromptSleep(session.ID, now, crewSleepPromptGrace) {
				continue
			}
		default:
			continue
		}
		d.actOnCrewMember(member, session.ID, action, cache, now)
	}
}

func (d *Daemon) actOnCrewMember(member crew.Member, sessionID string, action crew.Action, cache crew.CacheState, now time.Time) {
	switch action {
	case crew.ActionHeartbeat:
		session := d.store.Get(sessionID)
		if session == nil {
			return
		}
		generation := protocol.Deref(session.LastModelRequestAt)
		id := inputAttemptID("crew-heartbeat", sessionID+"/"+generation)
		delivery := maintenanceSessionInput("crew-heartbeat", sessionID+"/"+generation, sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		d.sessionInputs().forgetSuperseded(sessionID, id, delivery.origin)
		delivery.resend = func() {
			d.actOnCrewMember(member, sessionID, action, cache, time.Now())
		}
		attempt := d.sessionInputs().try(context.Background(), delivery)
		if attempt.err != nil {
			d.logf("crew: %s's heartbeat did not reach session %s: %v", crew.DisplayName(member.ID), sessionID, attempt.err)
			return
		}
		if attempt.stage != sessionInputTaken && sessionInputTakenWindow > 0 {
			attempt = d.sessionInputs().await(sessionID, id, attempt.wait, sessionInputTakenWindow)
		}
		if attempt.stage != sessionInputTaken {
			if attempt.stage == sessionInputPlaced {
				d.sessionInputs().relinquishComposer(sessionID, id)
			}
			d.logf("crew: %s's heartbeat was placed in session %s but no model request took it", crew.DisplayName(member.ID), sessionID)
			return
		}
		d.crewMemo().recordHeartbeat(sessionID, attempt.receipt.takenAt)
		d.sessionInputs().release(sessionID, id)
		d.logf("crew: warmed %s's context in session %s (cache estimated %s old against a %s assumption)",
			crew.DisplayName(member.ID), sessionID, cache.Age.Round(time.Second), cache.TTL)
	case crew.ActionSleep:
		delivery, _, err := d.store.EnqueueMaintenancePromptOnce(
			"crew-auto-sleep/"+uuid.NewString(),
			sessionID,
			member.ID,
			"crew-auto-sleep",
			crewSleepPrompt,
			now,
		)
		if err != nil {
			d.logf("crew: %s's sleep request could not be recorded: %v", crew.DisplayName(member.ID), err)
			return
		}
		if err := d.deliverAgentMailboxItem(delivery); err != nil {
			d.logf("crew: %s's sleep request is queued for session %s: %v", crew.DisplayName(member.ID), sessionID, err)
			return
		}
		d.logf("crew: asked %s to close its day — the user has been away and the cache is %s from lapsing",
			crew.DisplayName(member.ID), cache.Remaining().Round(time.Second))
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
