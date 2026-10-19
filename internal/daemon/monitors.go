package daemon

import (
	"crypto/sha256"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

type sessionMonitor struct {
	mu         sync.Mutex
	terminalID harness.TerminalID
	runtime    monitorRuntimeID
	terminal   sessionstate.TerminalEvidence
	entries    map[*monitorEntry]struct{}
}

type monitorEntry struct {
	mu                  sync.Mutex
	initialized         bool
	revision            uint64
	state               monitorStateView
	driver              monitorDriverCursor
	turn                monitorTurnStamps
	lastModelRequestAt  time.Time
	firstReportAt       time.Time
	wakeTimer           *time.Timer
	wake                func(time.Time) *time.Timer
	monitor             *sessionMonitor
	sink                monitorSink
	conversation        string
	localConversation   string
	evidence            sessionstate.ConversationEvidence
	resolveAt           time.Time
	resolvePending      bool
	dwell               dwellPending
	reason              string
	trace               *statetrace.Ring
	classifiedTurn      string
	classifyingTurn     string
	forcedStop          time.Time
	silence             *time.Timer
	input               *sessionInputLane
	autoSettle          *autoSettleTimer
	dismissal           autoSettleDismissal
	activityAt          time.Time
	transcriptWatch     *transcriptWatcher
	pluginUsageWatch    *pluginUsageWatcher
	finalUsage          []func()
	usageRuns           int
	usageIdle           chan struct{}
	usageDraining       bool
	pendingConversation *agentConversationObservation
	titleAttempted      bool
	titleInitialPrompt  *[sha256.Size]byte
	pluginLaunching     *pluginSessionLaunch
	pluginReports       []pendingPluginReport
	pluginExit          *ptybackend.ExitInfo
}

type monitorEvidence struct {
	sessionstate.TerminalEvidence
	sessionstate.ConversationEvidence
}

func (e *monitorEvidence) ClearProgramStatus() {
	sessionstate.ClearProgramStatus(&e.TerminalEvidence, &e.ConversationEvidence)
}

type monitorBinding struct {
	entry      *monitorEntry
	applied    monitorView
	catchUp    *time.Timer
	retryDelay time.Duration
}

type monitorRegistry struct {
	applyMu   sync.Mutex
	mu        sync.Mutex
	physical  map[harness.TerminalID]*sessionMonitor
	sessions  map[protocol.SessionID]*monitorBinding
	wake      chan struct{}
	closed    atomic.Bool
	nextLocal uint64
}

func (d *Daemon) monitors() *monitorRegistry {
	d.monitorsOnce.Do(func() {
		d.monitorState = &monitorRegistry{physical: make(map[harness.TerminalID]*sessionMonitor), sessions: make(map[protocol.SessionID]*monitorBinding), wake: make(chan struct{}, 1)}
	})
	return d.monitorState
}

func (r *monitorRegistry) entry(sessionID protocol.SessionID) *monitorEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding := r.sessions[sessionID]; binding != nil {
		return binding.entry
	}
	return nil
}

func (d *Daemon) monitorEntry(sessionID protocol.SessionID) *monitorEntry {
	return d.monitors().entry(sessionID)
}

func (r *monitorRegistry) start(terminal harness.TerminalID, conversation string, sessionID protocol.SessionID, linked bool, initialize func(*monitorEntry)) *monitorEntry {
	r.mu.Lock()
	var detached *monitorEntry
	defer func() {
		r.mu.Unlock()
		if detached != nil {
			detached.stop()
		}
	}()
	var old *monitorEntry
	if binding := r.sessions[sessionID]; binding != nil {
		old = binding.entry
	}
	if old != nil && old.monitor.terminalID == terminal {
		old.mu.Lock()
		defer old.mu.Unlock()
		if (conversation == "" && !linked) || old.conversation == conversation {
			initialize(old)
			return old
		}
		if old.conversation == "" && conversation != "" {
			old.conversation = conversation
			initialize(old)
			old.armWakeLocked()
			return old
		}
	}
	var m *sessionMonitor
	if !linked {
		m = r.physical[terminal]
	}
	if m == nil {
		m = &sessionMonitor{terminalID: terminal, entries: make(map[*monitorEntry]struct{})}
		if linked {
			m.runtime = monitorRuntimeID(uuid.NewString())
		}
	}
	detached = r.forgetLocked(sessionID)
	if !linked {
		r.physical[terminal] = m
	}
	r.nextLocal++
	e := &monitorEntry{monitor: m, conversation: conversation, localConversation: "local:" + strconv.FormatUint(r.nextLocal, 10)}
	e.input = &sessionInputLane{mu: &e.mu, entry: e, stopped: r.closed.Load()}
	initialize(e)
	m.entries[e] = struct{}{}
	r.sessions[sessionID] = &monitorBinding{entry: e, applied: e.snapshotLocked()}
	e.mu.Lock()
	e.armWakeLocked()
	e.mu.Unlock()
	return e
}

func (d *Daemon) startSessionMonitor(sessionID protocol.SessionID) *monitorEntry {
	return d.startSessionMonitorFrom(sessionID, "")
}

func (d *Daemon) startSessionMonitorFrom(sessionID, previousID protocol.SessionID) *monitorEntry {
	terminal := d.primaryTerminal(sessionID)
	conversation := d.store.GetSessionConversation(sessionID).NativeID
	linked := d.store.SessionLink(sessionID) == codexLinkKind
	if linked {
		if session := d.store.Get(sessionID); session != nil {
			terminal = codexServerTerminal(session.ProfileID)
		}
	}
	if terminal == "" {
		return nil
	}
	var previous *monitorEntry
	if previousID != "" {
		previous = d.monitorEntry(previousID)
	}
	entry := d.monitors().start(terminal, conversation, sessionID, linked, func(entry *monitorEntry) {
		d.initializeMonitor(entry, sessionID)
	})
	if previous != nil && previous != entry {
		previous.mu.Lock()
		typed := previous.input.userSubmit
		previous.input.userSubmit = false
		previous.mu.Unlock()
		if typed {
			entry.mu.Lock()
			entry.input.userSubmit = true
			entry.mu.Unlock()
		}
	}
	return entry
}

func (d *Daemon) initializeMonitor(entry *monitorEntry, sessionID protocol.SessionID) {
	if entry.sink == nil {
		entry.sink = monitorHome{daemon: d, entry: entry}
		entry.wake = func(deadline time.Time) *time.Timer {
			key := entry.keyLocked()
			return d.life.AfterFunc("monitorSnoozeWake", max(0, time.Until(deadline)), func() {
				crashAt(crashWhileWakingASnooze)
				view, woken := entry.command(monitorCommand{Key: key, Kind: monitorWake, Until: deadline}, deadline)
				if woken && entry.sink.Applied(view) {
					d.finishSnoozeWake(sessionID, deadline, "deadline")
				}
			})
		}
	}
	if !entry.initialized {
		if session := d.store.Get(sessionID); session != nil {
			entry.state = monitorStateView{State: session.State, Since: protocol.Timestamp(session.StateSince).Time(), Updated: protocol.Timestamp(session.StateUpdatedAt).Time()}
			entry.lastModelRequestAt = protocol.Timestamp(protocol.Deref(session.LastModelRequestAt)).Time()
		}
		stamps := d.store.TurnStamps(sessionID)
		cursor := d.store.GetAgentDriverRun(sessionID)
		entry.driver = monitorDriverCursor{Plugin: cursor.PluginName, Run: cursor.RunID, Sequence: cursor.Seq}
		entry.turn = monitorTurnStamps{OpenedAt: stamps.OpenedAt, SettledAt: stamps.SettledAt, SnoozedUntil: stamps.SnoozedUntil}
		entry.initialized = d.store.Get(sessionID) != nil
	}
}

func (r *monitorRegistry) each() map[protocol.SessionID]*monitorEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make(map[protocol.SessionID]*monitorEntry, len(r.sessions))
	for id, binding := range r.sessions {
		entries[id] = binding.entry
	}
	return entries
}

func (d *Daemon) forgetMonitor(sessionID protocol.SessionID) {
	r := d.monitors()
	r.mu.Lock()
	e := r.forgetLocked(sessionID)
	r.mu.Unlock()
	if e != nil {
		e.stop()
	}
	r.rearm()
}

func (r *monitorRegistry) forgetLocked(sessionID protocol.SessionID) *monitorEntry {
	binding := r.sessions[sessionID]
	if binding == nil {
		return nil
	}
	binding.stopCatchUp()
	e := binding.entry
	m := e.monitor
	delete(r.sessions, sessionID)
	delete(m.entries, e)
	if len(m.entries) == 0 && r.physical[m.terminalID] == m {
		delete(r.physical, m.terminalID)
	}
	return e
}

func (e *monitorEntry) stop() {
	release := e.finishUsage()
	defer release()
	e.mu.Lock()
	lane := e.input
	lane.stopRetriesLocked()
	if e.autoSettle != nil {
		e.autoSettle.timer.Stop()
		e.autoSettle = nil
	}
	if e.silence != nil {
		e.silence.Stop()
		e.silence = nil
	}
	if e.wakeTimer != nil {
		e.wakeTimer.Stop()
		e.wakeTimer = nil
	}
	e.resolvePending = false
	e.mu.Unlock()
	lane.running.Wait()
}

func (r *monitorRegistry) rearm() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
