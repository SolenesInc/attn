package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
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
	crewMailStartLimitDefault       = 8
	crewMailStartLimitWindowDefault = 43200
	crewMailStartLimitMax           = 1000
	crewMailStartLimitWindowMinSecs = 60
	crewMailStartLimitWindowMaxSecs = 7 * 24 * 3600
	crewCacheTTLMinSeconds          = 60
	crewCacheTTLMaxSeconds          = 24 * 3600
	crewHeartbeatLeadMinSeconds     = 30
	crewHeartbeatLeadMaxSeconds     = 3600
	crewAwayMinSeconds              = 60
	crewAwayMaxSeconds              = 24 * 3600
)

func crewHeartbeatPrompt() string { return prompts.RenderText("crew", "heartbeat", prompts.Values{}) }

const crewClosePromptGrace = 10 * time.Minute

type crewLifecycleMemo struct {
	mu                     sync.Mutex
	lastHeartbeat          map[protocol.SessionID]crewHeartbeat
	lastClosePromptAttempt map[protocol.SessionID]time.Time
}

type crewHeartbeat struct {
	generation string
	at         time.Time
}

func newCrewLifecycleMemo() *crewLifecycleMemo {
	return &crewLifecycleMemo{
		lastHeartbeat:          make(map[protocol.SessionID]crewHeartbeat),
		lastClosePromptAttempt: make(map[protocol.SessionID]time.Time),
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

func (m *crewLifecycleMemo) mayPromptClose(sessionID protocol.SessionID, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.lastClosePromptAttempt[sessionID]; ok && now.Sub(last) < grace {
		return false
	}
	m.lastClosePromptAttempt[sessionID] = now
	return true
}

func (m *crewLifecycleMemo) forget(sessionID protocol.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.lastHeartbeat, sessionID)
	delete(m.lastClosePromptAttempt, sessionID)
}

func (d *Daemon) crewBoolSetting(name *setting.Setting) bool {
	if d.store == nil {
		return true
	}
	return d.on(name)
}

func (d *Daemon) crewSeconds(name *setting.Setting, fallback, min, max int) time.Duration {
	seconds := fallback
	if d.store != nil {
		raw := strings.TrimSpace(d.setting(name))
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
		if raw := strings.TrimSpace(d.setting(setting.CrewCacheTTLFor.For(agent))); raw != "" {
			return d.crewSeconds(setting.CrewCacheTTLFor.For(agent), fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
		}
	}
	return d.crewSeconds(setting.CrewCacheTTLSeconds, fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
}

func (d *Daemon) crewHeartbeatLead() time.Duration {
	return d.crewSeconds(setting.CrewHeartbeatLeadSeconds, crewHeartbeatLeadDefault, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
}

func (d *Daemon) crewAwayLimit() time.Duration {
	return d.crewSeconds(setting.CrewAwaySeconds, crewAwayDefault, crewAwayMinSeconds, crewAwayMaxSeconds)
}

func (d *Daemon) crewStartLedger() crew.MailStartLedger {
	limit := crewMailStartLimitDefault
	if d.store != nil {
		if raw := strings.TrimSpace(d.setting(setting.CrewMailStartLimit)); raw != "" {
			limit = resolveBoundedIntSetting(raw, crewMailStartLimitDefault, 0, crewMailStartLimitMax)
		}
	}
	return crew.MailStartLedger{
		Limit:  limit,
		Window: d.crewSeconds(setting.CrewMailStartWindowSeconds, crewMailStartLimitWindowDefault, crewMailStartLimitWindowMinSecs, crewMailStartLimitWindowMaxSecs),
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
	return sessionmonitor.PhaseAllows(sessionmonitor.AtTurnBoundary, session.State)
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
	heartbeat := d.crewBoolSetting(setting.CrewHeartbeatEnabled)
	awayClose := d.crewBoolSetting(setting.CrewAwayCloseEnabled)
	for _, member := range members {
		if member.Chief || !d.crewBindingLive(member) {
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
			RunFailed:        attention.RunFailed(d.stateReason(session.ID)),
			HeartbeatEnabled: heartbeat,
			AwayCloseEnabled: awayClose,
		})
		if action == crew.ActionClose && !d.crewMemo().mayPromptClose(session.ID, now, crewClosePromptGrace) {
			continue
		}
		d.actOnCrewMember(member, session.ID, action, cache, now)
	}
}

func (d *Daemon) actOnCrewMember(member crew.Member, sessionID protocol.SessionID, action crew.Action, cache crew.CacheState, now time.Time) {
	if d.unreachableSessionHost(sessionID) != "" {
		return
	}
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
		delivery := sessionmonitor.MaintenanceInput(sessionmonitor.CrewHeartbeat, string(sessionID)+"/"+generation, crewHeartbeatPrompt(), sessionmonitor.WhenPromptReady)
		attempt := d.placeDelivery(context.Background(), sessionID, delivery)
		if attempt.Stage != sessionmonitor.Placed {
			if after, retry := sessionmonitor.RetryDelay(attempt.Err); retry {
				d.life.AfterFunc("crewHeartbeatRetry", after, func() { d.actOnCrewMember(member, sessionID, action, cache, time.Now()) })
			}
			d.logf("crew: %s's heartbeat did not reach session %s: %v", member.Name, sessionID, attempt.Err)
			return
		}
		d.crewMemo().recordHeartbeat(sessionID, generation, attempt.At)
		d.logf("crew: warmed %s's context in session %s (cache estimated %s old against a %s assumption)",
			member.Name, sessionID, cache.Age.Round(time.Second), cache.TTL)
	case crew.ActionClose:
		session := d.store.Get(sessionID)
		if session == nil {
			return
		}
		generation := protocol.Deref(session.LastModelRequestAt)
		d.clearCrewSwitching(member.ID)
		prompt := prompts.RenderText("crew", "close-away", prompts.Values{})
		receipt, err := d.sendToInbox(inbox.Item{ID: "crew-away-close/" + string(sessionID) + "/" + generation, To: who.ToSession(sessionID), Kind: inbox.Notice, Source: member.ID, Key: "crew-away-close", Text: prompt})
		if err != nil {
			d.logf("crew: %s's close request could not be recorded: %v", member.Name, err)
			return
		}
		if !receipt.Rang {
			return
		}

		d.logf("crew: asked %s to close its day — the user has been away and the cache is %s from lapsing",
			member.Name, cache.Remaining().Round(time.Second))
	}
}

func (d *Daemon) crewMemo() *crewLifecycleMemo {
	d.crewMemoOnce.Do(func() { d.crewLifecycleState = newCrewLifecycleMemo() })
	return d.crewLifecycleState
}

func (d *Daemon) chargeMailStart(memberID string, now time.Time) error {
	ledger := d.crewStartLedger()
	var refusal error
	if _, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		ledger.Stamps = parseStartStamps(member.MailStarts)
		kept, err := ledger.Allows(member.Name, now)
		if err != nil {
			refusal = err
			return false, nil
		}
		member.MailStarts = formatStartStamps(kept)
		return true, nil
	}); err != nil {
		return err
	}
	if refusal != nil {
		d.logf("crew: %v", refusal)
	}
	return refusal
}

func parseStartStamps(raw []string) []time.Time {
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

func formatStartStamps(stamps []time.Time) []string {
	out := make([]string, 0, len(stamps))
	for _, at := range stamps {
		out = append(out, at.UTC().Format(time.RFC3339))
	}
	return out
}
