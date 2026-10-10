package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

const (
	crewLifecycleKind     = "crew_lifecycle_tick"
	crewLifecycleInterval = 60 * time.Second
	crewLifecycleTimeout  = 60 * time.Second
)

const (
	crewCacheTTLClaude  = 3600
	crewCacheTTLCodex   = 1800
	crewCacheTTLDefault = 3600
)

const crewHeartbeatLeadDefault = 300

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
	lastHeartbeat          map[protocol.SessionID]crewHeartbeat
	lastSleepPromptAttempt map[protocol.SessionID]time.Time
}

type crewHeartbeat struct {
	generation string
	at         time.Time
}

func newCrewLifecycleMemo() *crewLifecycleMemo {
	return &crewLifecycleMemo{
		lastHeartbeat:          make(map[protocol.SessionID]crewHeartbeat),
		lastSleepPromptAttempt: make(map[protocol.SessionID]time.Time),
	}
}

// heartbeatDue never sends one cache generation a second heartbeat: attn cannot tell whether the agent missed the first.
func (m *crewLifecycleMemo) heartbeatDue(sessionID protocol.SessionID, generation string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastHeartbeat[sessionID]
	return !ok || (last.generation != generation && now.Sub(last.at) >= grace)
}

func (m *crewLifecycleMemo) recordHeartbeat(sessionID protocol.SessionID, generation string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastHeartbeat[sessionID] = crewHeartbeat{generation: generation, at: at}
}

func (m *crewLifecycleMemo) mayPromptSleep(sessionID protocol.SessionID, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.lastSleepPromptAttempt[sessionID]; ok && now.Sub(last) < grace {
		return false
	}
	m.lastSleepPromptAttempt[sessionID] = now
	return true
}

func (m *crewLifecycleMemo) forget(sessionID protocol.SessionID) {
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
	state := crew.CacheState{TTL: d.crewCacheTTL(session.Agent)}
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
		if action == crew.ActionSleep && !d.crewMemo().mayPromptSleep(session.ID, now, crewSleepPromptGrace) {
			continue
		}
		d.actOnCrewMember(member, session.ID, action, cache, now)
	}
}

func (d *Daemon) actOnCrewMember(member crew.Member, sessionID protocol.SessionID, action crew.Action, cache crew.CacheState, now time.Time) {
	switch action {
	case crew.ActionHeartbeat:
		session := d.store.Get(sessionID)
		if session == nil {
			return
		}
		generation := protocol.Deref(session.LastModelRequestAt)
		if !d.crewMemo().heartbeatDue(sessionID, generation, now, d.crewHeartbeatLead()) {
			return
		}
		delivery := maintenanceSessionInput("crew-heartbeat", string(sessionID)+"/"+generation, sessionID, crewHeartbeatPrompt, sessionInputWhenPromptReady)
		delivery.resend = func() {
			d.actOnCrewMember(member, sessionID, action, cache, time.Now())
		}
		attempt := d.sessionInputs().try(context.Background(), delivery)
		if attempt.stage != sessionInputPlaced {
			d.logf("crew: %s's heartbeat did not reach session %s: %v", member.Key.String(), sessionID, attempt.err)
			return
		}
		d.crewMemo().recordHeartbeat(sessionID, generation, attempt.at)
		d.logf("crew: warmed %s's context in session %s (cache estimated %s old against a %s assumption)",
			d.storedMemberName(member.Key.String()), sessionID, cache.Age.Round(time.Second), cache.TTL)
	case crew.ActionSleep:
		session := d.store.Get(sessionID)
		if session == nil {
			return
		}
		generation := protocol.Deref(session.LastModelRequestAt)
		receipt, err := d.sendToInbox(inbox.Item{ID: "crew-auto-sleep/" + string(sessionID) + "/" + generation, To: inbox.ToSession(sessionID), Kind: inbox.Notice, Source: member.Key.String(), Key: "crew-auto-sleep", Text: crewSleepPrompt})
		if err != nil {
			d.logf("crew: %s's sleep request could not be recorded: %v", member.Key.String(), err)
			return
		}
		if !receipt.Rang {
			return
		}

		d.logf("crew: asked %s to close its day — the user has been away and the cache is %s from lapsing",
			d.storedMemberName(member.Key.String()), cache.Remaining().Round(time.Second))
	}
}

func (d *Daemon) crewMemo() *crewLifecycleMemo {
	d.crewMemoOnce.Do(func() { d.crewLifecycleState = newCrewLifecycleMemo() })
	return d.crewLifecycleState
}

func (d *Daemon) chargeAutonomousWake(key who.MemberKey, now time.Time) error {
	ledger := d.crewWakeLedger()
	var refusal error
	if _, err := d.updateCrewMember(key, func(member *crew.Member) (bool, error) {
		ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
		kept, err := ledger.Allows(d.memberName(member.Key), now)
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
