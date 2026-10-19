package daemon

import (
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

type monitorRuntimeID string

type monitorKey struct {
	Terminal     harness.TerminalID
	Runtime      monitorRuntimeID
	Conversation string
}

type monitorValue[T any] struct{ V T }
type monitorKeyedSet[K comparable, V any] struct{ Entries map[K]V }

type monitorPullRequest struct {
	Repository string
	Number     int
	URL        string
	Sequence   uint64
}
type monitorStateView struct {
	Quiet     bool
	Origin    stateOrigin
	Cause     string
	Sequence  uint64
	Touch     bool
	Broadcast bool
	Resolve   bool
	State     protocol.SessionState
	Since     time.Time
	Updated   time.Time
	Reason    string
}

type monitorTurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
	AgentAt      time.Time
}

type monitorAutoSettleView struct {
	FiresAt      time.Time
	Held         bool
	DismissArmed bool
}

type monitorDriverMetadata struct {
	Plugin         string
	Run            string
	Sequence       uint64
	Metadata       string
	TranscriptPath string
}

type monitorConversation struct {
	agentConversationObservation
	Claim    bool
	Terminal harness.TerminalID
}

type monitorExit struct {
	LifecycleID string
	Text        string
	Cols        int
	Rows        int
	Code        int
	Signal      string
	At          time.Time
}

type monitorView struct {
	Conversation       monitorValue[monitorConversation]                 `fact:"session.conversation.changed"`
	CostInitialized    monitorValue[bool]                                `fact:"session.cost.changed"`
	Cost               monitorKeyedSet[string, *store.SessionCostSource] `fact:"session.cost.changed"`
	FirstReportAt      monitorValue[time.Time]                           `fact:"-"`
	Key                monitorValue[monitorKey]                          `fact:"-"`
	Revision           monitorValue[uint64]                              `fact:"-"`
	Succeeds           monitorValue[monitorKey]                          `fact:"-"`
	State              monitorValue[monitorStateView]                    `fact:"session.state.changed"`
	Turn               monitorValue[monitorTurnStamps]                   `fact:"session.state.changed"`
	AutoSettle         monitorValue[monitorAutoSettleView]               `fact:"session.state.changed"`
	LastModelRequestAt monitorValue[time.Time]                           `fact:"session.model_request.started"`
	Driver             monitorValue[monitorDriverMetadata]               `fact:"session.state.changed"`
	Branch             monitorValue[attngit.BranchInfo]                  `fact:"session.branch.changed"`
	PullRequests       monitorKeyedSet[string, monitorPullRequest]       `fact:"session.pull_request.changed"`
	SentFiles          monitorKeyedSet[string, uint64]                   `fact:"-"`
	HarnessTitle       monitorValue[string]                              `fact:"session.renamed"`
	Exit               monitorValue[*monitorExit]                        `fact:"session.pty.exited"`
	Live               monitorValue[bool]                                `fact:"-"`
}

type monitorSink interface{ Applied(monitorView) bool }

func (e *monitorEntry) viewLocked() monitorView {
	e.revision++
	return e.snapshotLocked()
}

func (e *monitorEntry) snapshotLocked() monitorView {
	auto := monitorAutoSettleView{DismissArmed: e.dismissal != autoSettleDismissalOff}
	if timer := e.autoSettle; timer != nil {
		if timer.phase == autoSettleCounting {
			auto.FiresAt = timer.firesAt
		} else {
			auto.Held = timer.visible()
		}
	}
	state := e.state
	state.Reason = e.reason
	e.monitor.mu.Lock()
	process := e.monitor.terminal.Process
	live := e.exit == nil && (process == nil || process.Claim != sessionstate.ClaimExited)
	e.monitor.mu.Unlock()
	return monitorView{
		Conversation:       monitorValue[monitorConversation]{e.conversationInput},
		Succeeds:           monitorValue[monitorKey]{e.succeeds},
		CostInitialized:    monitorValue[bool]{e.costInitialized},
		Cost:               monitorKeyedSet[string, *store.SessionCostSource]{Entries: e.cost},
		Key:                monitorValue[monitorKey]{e.keyLocked()},
		Revision:           monitorValue[uint64]{e.revision},
		State:              monitorValue[monitorStateView]{state},
		Turn:               monitorValue[monitorTurnStamps]{e.turn},
		AutoSettle:         monitorValue[monitorAutoSettleView]{auto},
		LastModelRequestAt: monitorValue[time.Time]{e.lastModelRequestAt},
		Driver:             monitorValue[monitorDriverMetadata]{e.driver},
		FirstReportAt:      monitorValue[time.Time]{e.firstReportAt},
		Branch:             monitorValue[attngit.BranchInfo]{e.branch},
		PullRequests:       monitorKeyedSet[string, monitorPullRequest]{Entries: e.pullRequests},
		SentFiles:          monitorKeyedSet[string, uint64]{Entries: e.sentFiles},
		HarnessTitle:       monitorValue[string]{e.harnessTitle},
		Exit:               monitorValue[*monitorExit]{e.exit},
		Live:               monitorValue[bool]{live},
	}
}

func (e *monitorEntry) keyLocked() monitorKey {
	conversation := e.conversation
	if conversation == "" {
		conversation = e.localConversation
	}
	return monitorKey{Terminal: e.monitor.terminalID, Runtime: e.monitor.runtime, Conversation: conversation}
}

func (e *monitorEntry) changeStateLocked(state string, cause sessionStateCause, request, now time.Time) bool {
	if driver, ok := cause.(linkTurn); ok {
		if driver.run == "" || driver.seq == 0 || e.driver.Run != driver.run || e.driver.Sequence >= driver.seq {
			return false
		}
		e.driver.Sequence = driver.seq
		if request.After(e.lastModelRequestAt) {
			e.lastModelRequestAt = request
		}
	}
	effects, _ := stateEffectInstanceFor(cause)
	_, resolved := cause.(resolverObservation)
	e.state = monitorStateView{Sequence: e.state.Sequence + 1, Touch: effects.touch, Broadcast: effects.broadcast, Resolve: !resolved, State: protocol.SessionState(state), Since: now, Updated: now, Reason: e.reason}
	return true
}

func (d *Daemon) monitorViewFor(session protocol.SessionID) (monitorView, bool) {
	r := d.monitors()
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	binding := r.sessions[session]
	if binding == nil {
		return monitorView{}, false
	}
	return binding.applied, true
}
