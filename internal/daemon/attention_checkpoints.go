package daemon

import (
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/stepclock"
)

const (
	AttentionStopTimer     stepclock.Name = "attention_stop"
	AttentionElapsedTimer  stepclock.Name = "attention_elapsed"
	AttentionFollowUpTimer stepclock.Name = "attention_follow_up"

	// Rings start 606 of 708 runs after a quiet stop, at a median of 2 to 10 min; a 15 min wait cuts verdicts
	// from 146 to 65 a day on ai-sandbox (quiet-stop-report.md on s-95qsxv).
	attentionStopWait = 15 * time.Minute
	// D8 on s-4kr3bn sets the window.
	attentionFollowUpWindow = 30 * time.Minute
)

// Of 1,923 background waits, 149 were notified after 20 min or more, 39 after 60 min and 8 after 4 h
// (stuck_working.md on s-45t02j).
var attentionElapsedSteps = []struct {
	after      time.Duration
	checkpoint attention.Checkpoint
}{
	{20 * time.Minute, attention.CheckpointElapsed20m},
	{time.Hour, attention.CheckpointElapsed60m},
	{4 * time.Hour, attention.CheckpointElapsed4h},
}

const (
	followUpTyped   = "typed"
	followUpClosed  = "closed"
	followUpNothing = "nothing"
	followUpUnknown = "unknown"
)

var attentionFactChanges = bus.Filter{
	"garden.seed.harvested", "garden.seed.withered", "garden.seed.parked", "garden.seed.replanted", "garden.seed.tended",
	FactSeedStandbyViewChanged, FactSessionClosed,
}

type attentionCheckpoints struct {
	mu          sync.Mutex
	judging     sync.Mutex
	lastFactAt  time.Time
	unsubscribe func()
	stops       map[protocol.SessionID]stepclock.Timer
	elapsed     map[protocol.SessionID]elapsedCheckpoint
	judged      map[protocol.SessionID]judgedFacts
	followUps   map[protocol.SessionID][]*attentionFollowUp
}

type elapsedCheckpoint struct {
	timer stepclock.Timer
	since time.Time
	step  int
}

type judgedFacts struct {
	key      string
	subjects []string
}

type attentionFollowUp struct {
	verdictAt  time.Time
	checkpoint attention.Checkpoint
	verdict    attention.Verdict
	timer      stepclock.Timer
}

type attentionVerdictProps struct {
	Checkpoint attention.Checkpoint `json:"checkpoint"`
	Verdict    attention.Kind       `json:"verdict"`
	Decider    string               `json:"decider"`
	Label      attention.Label      `json:"label,omitempty"`
	Failure    attention.Failure    `json:"failure,omitempty"`
	LatencyMs  int64                `json:"latency_ms"`
	FollowUp   string               `json:"follow_up"`
}

func (d *Daemon) subscribeAttentionFacts() {
	d.attention.unsubscribe = d.eventBus.Subscribe(attentionFactChanges, d.attentionFactChanged)
}

func (d *Daemon) unsubscribeAttentionFacts() {
	if d.attention.unsubscribe != nil {
		d.attention.unsubscribe()
		d.attention.unsubscribe = nil
	}
}

func (d *Daemon) attentionWatches(session *protocol.Session) bool {
	return session != nil && session.Agent != protocol.AgentShellValue && session.CrewMember == nil && session.Automation == nil &&
		!d.monitors().copied(session.ID) && d.runsHomeRole()
}

func stoppedWithNoTurn(view sessionmonitor.View, now time.Time) bool {
	turn := view.Turn.V
	switch view.State.V.State {
	case protocol.SessionStateIdle, protocol.SessionStateWaitingInput:
	default:
		return false
	}
	return !turn.OpenedAt.After(turn.SettledAt) && !turn.SnoozedUntil.After(now)
}

func waitsOnBackground(view sessionmonitor.View) bool {
	return len(view.BackgroundTasks.V) > 0 || view.State.V.Reason == string(sessionstate.ReasonBackgroundParked)
}

func (d *Daemon) observeAttention(session protocol.SessionID, previous, view sessionmonitor.View) {
	if !d.attentionWatches(d.store.Get(session)) {
		return
	}
	state, before := view.State.V, previous.State.V
	if state.Sequence != before.Sequence {
		if state.State == protocol.SessionStateWorking && before.State != protocol.SessionStateWorking && !view.NotificationRun.V {
			d.resolveAttentionFollowUps(session, followUpTyped)
		}
		d.armAttentionStop(session, view)
	}
	d.armAttentionElapsed(session, view)
}

func (d *Daemon) armAttentionStop(session protocol.SessionID, view sessionmonitor.View) {
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	if timer := a.stops[session]; timer != nil {
		timer.Stop()
		delete(a.stops, session)
	}
	if !stoppedWithNoTurn(view, d.clock.Now()) {
		return
	}
	stopped := view.State.V.Sequence
	if a.stops == nil {
		a.stops = map[protocol.SessionID]stepclock.Timer{}
	}
	a.stops[session] = d.clock.AfterFunc(AttentionStopTimer, attentionStopWait, func() {
		d.life.Do("attentionStop", func() {
			a.mu.Lock()
			delete(a.stops, session)
			a.mu.Unlock()
			current, _ := d.monitorViewFor(session)
			if current.State.V.Sequence == stopped && stoppedWithNoTurn(current, d.clock.Now()) {
				d.attentionCheckpoint(session, attention.CheckpointStop)
			}
		})
	})
}

func (d *Daemon) armAttentionElapsed(session protocol.SessionID, view sessionmonitor.View) {
	since := view.LastModelRequestAt.V
	if since.IsZero() {
		since = view.State.V.Since
	}
	step := slices.IndexFunc(attentionElapsedSteps, func(s struct {
		after      time.Duration
		checkpoint attention.Checkpoint
	}) bool {
		return since.Add(s.after).After(d.clock.Now())
	})
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	armed, ok := a.elapsed[session]
	if ok && armed.since.Equal(since) && armed.step == step && waitsOnBackground(view) {
		return
	}
	if ok {
		armed.timer.Stop()
		delete(a.elapsed, session)
	}
	if step < 0 || !waitsOnBackground(view) {
		return
	}
	checkpoint := attentionElapsedSteps[step].checkpoint
	if a.elapsed == nil {
		a.elapsed = map[protocol.SessionID]elapsedCheckpoint{}
	}
	timer := d.clock.AfterFunc(AttentionElapsedTimer, since.Add(attentionElapsedSteps[step].after).Sub(d.clock.Now()), func() {
		d.life.Do("attentionElapsed", func() {
			current, _ := d.monitorViewFor(session)
			if waitsOnBackground(current) && (current.LastModelRequestAt.V.Equal(since) || current.LastModelRequestAt.V.IsZero()) {
				d.attentionCheckpoint(session, checkpoint)
			}
			d.armAttentionElapsed(session, current)
		})
	})
	a.elapsed[session] = elapsedCheckpoint{timer: timer, since: since, step: step}
}

func (d *Daemon) attentionFactChanged(event bus.Event) {
	if event.Name == FactSessionClosed {
		d.forgetAttention(protocol.SessionID(event.Subject))
	}
	a := &d.attention
	a.mu.Lock()
	var touched []protocol.SessionID
	for session, judged := range a.judged {
		if slices.Contains(judged.subjects, event.Subject) && a.stops[session] == nil {
			touched = append(touched, session)
		}
	}
	a.mu.Unlock()
	if len(touched) == 0 {
		return
	}
	d.life.Go("attentionFactChange", func() {
		for _, session := range touched {
			if view, _ := d.monitorViewFor(session); stoppedWithNoTurn(view, d.clock.Now()) {
				d.attentionCheckpoint(session, attention.CheckpointFactChange)
			}
		}
	})
}

func (d *Daemon) forgetAttention(session protocol.SessionID) {
	d.resolveAttentionFollowUps(session, followUpClosed)
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	if timer := a.stops[session]; timer != nil {
		timer.Stop()
	}
	if armed, ok := a.elapsed[session]; ok {
		armed.timer.Stop()
	}
	delete(a.stops, session)
	delete(a.elapsed, session)
	delete(a.judged, session)
}

func (d *Daemon) startAttentionCheckpoints() {
	d.loadAttentionLog()
	d.life.Go("attentionDaemonStart", func() {
		now := d.clock.Now()
		for _, session := range d.store.List("") {
			if !d.attentionWatches(session) {
				continue
			}
			view, _ := d.monitorViewFor(session.ID)
			d.armAttentionElapsed(session.ID, view)
			if !stoppedWithNoTurn(view, now) {
				continue
			}
			if remaining := attentionStopWait - now.Sub(view.State.V.Since); remaining > 0 {
				d.armAttentionStop(session.ID, view)
				continue
			}
			d.attentionCheckpoint(session.ID, attention.CheckpointDaemonStart)
		}
	})
}

func (d *Daemon) attentionCheckpoint(session protocol.SessionID, checkpoint attention.Checkpoint) {
	record := d.store.Get(session)
	if !d.attentionWatches(record) {
		return
	}
	a := &d.attention
	a.judging.Lock()
	defer a.judging.Unlock()
	ctx := d.life.Context()
	now := d.clock.Now()
	facts := d.attentionFacts(ctx, record, now)
	facts.Checkpoint = checkpoint
	if checkpoint == attention.CheckpointDaemonStart && !a.lastFactAt.IsZero() {
		facts.DaemonDownFor = protocol.Ptr(attention.AgeOf(a.lastFactAt, now))
	}
	view, _ := d.monitorViewFor(session)
	key := attentionKey(facts, view)
	a.mu.Lock()
	seen := a.judged[session].key == key
	a.mu.Unlock()
	if seen {
		return
	}
	verdict := d.attentionSupervisor(record.Agent).Judge(ctx, facts)
	d.recordAttentionVerdict(session, now, facts, key, verdict)
}

func attentionKey(facts attention.Facts, view sessionmonitor.View) string {
	key := facts.Fingerprint() + "|" + view.LastModelRequestAt.V.UTC().Format(time.RFC3339Nano) + "|" + view.Turn.V.SettledAt.UTC().Format(time.RFC3339Nano)
	if facts.Checkpoint.Elapsed() {
		key += "|" + string(facts.Checkpoint)
	}
	return key
}

func attentionSubjects(facts attention.Facts) []string {
	var subjects []string
	if facts.Stake != nil {
		subjects = append(subjects, facts.Stake.ID)
	}
	for _, seed := range facts.Tended {
		subjects = append(subjects, seed.ID)
	}
	for _, wake := range facts.Wakes {
		if wake.Seed != "" {
			subjects = append(subjects, wake.Seed)
		}
	}
	for _, delegate := range facts.Delegates {
		subjects = append(subjects, string(delegate.Session))
		if delegate.Stake != "" {
			subjects = append(subjects, delegate.Stake)
		}
	}
	return subjects
}

func (d *Daemon) recordAttentionVerdict(session protocol.SessionID, at time.Time, facts attention.Facts, key string, verdict attention.Verdict) {
	a := &d.attention
	a.mu.Lock()
	if a.judged == nil {
		a.judged = map[protocol.SessionID]judgedFacts{}
	}
	a.judged[session] = judgedFacts{key: key, subjects: attentionSubjects(facts)}
	a.mu.Unlock()
	encoded, _ := json.Marshal(facts)
	d.appendAttentionLog(attention.LogEntry{Kind: attention.LogVerdict, At: at, Session: session, Checkpoint: facts.Checkpoint, Key: key,
		Verdict: verdict.Kind, Decider: verdict.Decider, Label: verdict.Label, Reason: verdict.Reason, Failure: verdict.Failure, LatencyMs: verdict.Latency.Milliseconds(), Facts: encoded})
	d.logf("attention: session=%s checkpoint=%s %s", session, facts.Checkpoint, verdict)
	followUp := &attentionFollowUp{verdictAt: at, checkpoint: facts.Checkpoint, verdict: verdict}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.followUps == nil {
		a.followUps = map[protocol.SessionID][]*attentionFollowUp{}
	}
	a.followUps[session] = append(a.followUps[session], followUp)
	followUp.timer = d.clock.AfterFunc(AttentionFollowUpTimer, attentionFollowUpWindow, func() {
		d.life.Do("attentionFollowUp", func() { d.resolveAttentionFollowUp(session, followUp, followUpNothing) })
	})
}

func (d *Daemon) resolveAttentionFollowUps(session protocol.SessionID, outcome string) {
	a := &d.attention
	a.mu.Lock()
	pending := slices.Clone(a.followUps[session])
	a.mu.Unlock()
	for _, followUp := range pending {
		d.resolveAttentionFollowUp(session, followUp, outcome)
	}
}

func (d *Daemon) resolveAttentionFollowUp(session protocol.SessionID, followUp *attentionFollowUp, outcome string) {
	a := &d.attention
	a.mu.Lock()
	pending := a.followUps[session]
	index := slices.Index(pending, followUp)
	if index >= 0 {
		a.followUps[session] = slices.Delete(pending, index, index+1)
	}
	a.mu.Unlock()
	if index < 0 {
		return
	}
	followUp.timer.Stop()
	d.recordAttentionFollowUp(session, followUp.verdictAt, followUp.checkpoint, followUp.verdict, outcome)
}

func (d *Daemon) recordAttentionFollowUp(session protocol.SessionID, verdictAt time.Time, checkpoint attention.Checkpoint, verdict attention.Verdict, outcome string) {
	d.appendAttentionLog(attention.LogEntry{Kind: attention.LogFollowUp, At: d.clock.Now(), Session: session, VerdictAt: &verdictAt, FollowUp: outcome})
	if !d.analyticsCapturedSince(verdictAt) {
		return
	}
	props := attentionVerdictProps{Checkpoint: checkpoint, Verdict: verdict.Kind, Decider: verdict.Decider, Label: verdict.Label, Failure: verdict.Failure,
		LatencyMs: verdict.Latency.Milliseconds(), FollowUp: outcome}
	if err := d.appendAnalyticsAt(analytics.StreamRun, "attention_verdict", string(session), verdictAt, props); err != nil {
		d.logf("analytics: %v", err)
	}
}
