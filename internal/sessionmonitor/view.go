package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/sessioncost"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type Key struct {
	Terminal     harness.TerminalID
	Conversation string
}

type Value[T any] struct{ V T }
type KeyedSet[K comparable, V any] struct{ Entries map[K]V }

type PullRequest struct {
	Repository string
	Number     int
	URL        string
	Sequence   uint64
}
type StateView struct {
	Quiet     bool
	Origin    Origin
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

type TurnStamps struct {
	OpenedAt     time.Time
	SettledAt    time.Time
	SnoozedUntil time.Time
	AgentAt      time.Time
	RequestedAt  time.Time
}

type AutoSettleView struct {
	FiresAt      time.Time
	Held         bool
	DismissArmed bool
}

type DriverMetadata struct {
	Plugin         string
	Run            string
	Sequence       uint64
	Metadata       string
	TranscriptPath string
}

type ConversationObservation struct {
	NativeID       string
	TranscriptPath string
}

type ConversationProposal int

const (
	ConversationObserved ConversationProposal = iota
	ConversationReported
)

type Conversation struct {
	ConversationObservation
	Proposal ConversationProposal
	Terminal harness.TerminalID
}

type Exit struct {
	LifecycleID string
	Terminal    harness.TerminalID
	Stopped     bool
	Held        bool
	Text        string
	Cols        int
	Rows        int
	Code        int
	Signal      string
	At          time.Time
}

func SameExit(a, b *Exit) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	x.At, y.At = time.Time{}, time.Time{}
	return x == y && a.At.Equal(b.At)
}

type View struct {
	Conversation       Value[Conversation]                        `fact:"session.conversation.changed"`
	CostInitialized    Value[bool]                                `fact:"session.cost.changed"`
	Cost               KeyedSet[string, sessioncost.SourceTotals] `fact:"session.cost.changed"`
	Run                Value[uint64]                              `fact:"-"`
	NotificationRun    Value[bool]                                `fact:"-"`
	FirstReportAt      Value[time.Time]                           `fact:"-"`
	Key                Value[Key]                                 `fact:"-"`
	Epoch              Value[string]                              `fact:"-"`
	Revision           Value[uint64]                              `fact:"-"`
	Succeeds           Value[Key]                                 `fact:"-"`
	State              Value[StateView]                           `fact:"session.state.changed"`
	Turn               Value[TurnStamps]                          `fact:"session.state.changed"`
	AutoSettle         Value[AutoSettleView]                      `fact:"session.state.changed"`
	LastModelRequestAt Value[time.Time]                           `fact:"session.model_request.started"`
	Driver             Value[DriverMetadata]                      `fact:"session.state.changed"`
	Branch             Value[attngit.BranchInfo]                  `fact:"session.branch.changed"`
	PullRequests       KeyedSet[string, PullRequest]              `fact:"session.pull_request.changed"`
	SentFiles          KeyedSet[string, uint64]                   `fact:"-"`
	HarnessTitle       Value[string]                              `fact:"session.renamed"`
	TerminalTitle      Value[string]                              `fact:"session.title.changed"`
	LastSeen           Value[time.Time]                           `fact:"-"`
	PendingSwitch      Value[Conversation]                        `fact:"-"`
	Exit               Value[*Exit]                               `fact:"session.pty.exited"`
	Live               Value[bool]                                `fact:"-"`
	AssistantWindow    Value[uint64]                              `fact:"session.assistant_window.changed"`
}

func (m *Monitor) viewLocked() View {
	m.revision++
	return m.snapshotLocked()
}

func (m *Monitor) snapshotLocked() View {
	auto := AutoSettleView{DismissArmed: m.dismissal != autoSettleDismissalOff}
	if timer := m.autoSettle; timer != nil {
		if timer.phase == autoSettleCounting {
			auto.FiresAt = timer.firesAt
		} else {
			auto.Held = timer.visible()
		}
	}
	state := m.state
	state.Reason = m.reason
	m.watch.mu.Lock()
	process := m.watch.terminal.Process
	live := m.exit == nil && (process == nil || process.Claim != sessionstate.ClaimExited)
	m.watch.mu.Unlock()
	return View{
		Conversation:       Value[Conversation]{m.conversationInput},
		Succeeds:           Value[Key]{m.succeeds},
		CostInitialized:    Value[bool]{m.costInitialized},
		Cost:               KeyedSet[string, sessioncost.SourceTotals]{Entries: m.costTotals},
		Key:                Value[Key]{m.keyLocked()},
		Epoch:              Value[string]{m.epoch},
		Revision:           Value[uint64]{m.revision},
		State:              Value[StateView]{state},
		Turn:               Value[TurnStamps]{m.turn},
		AutoSettle:         Value[AutoSettleView]{auto},
		LastModelRequestAt: Value[time.Time]{m.lastModelRequestAt},
		Driver:             Value[DriverMetadata]{m.driver},
		FirstReportAt:      Value[time.Time]{m.firstReportAt},
		Run:                Value[uint64]{m.input.epoch},
		NotificationRun:    Value[bool]{m.input.inNotificationRunLocked()},
		Branch:             Value[attngit.BranchInfo]{m.branch},
		PullRequests:       KeyedSet[string, PullRequest]{Entries: m.pullRequests},
		SentFiles:          KeyedSet[string, uint64]{Entries: m.sentFiles},
		HarnessTitle:       Value[string]{m.harnessTitle},
		TerminalTitle:      Value[string]{m.terminalTitle},
		LastSeen:           Value[time.Time]{m.touchedAt},
		PendingSwitch:      Value[Conversation]{m.pendingHeadLocked()},
		Exit:               Value[*Exit]{m.exit},
		Live:               Value[bool]{live},
		AssistantWindow:    Value[uint64]{m.windowRevision},
	}
}

func (m *Monitor) keyLocked() Key {
	conversation := m.conversation
	if conversation == "" {
		conversation = m.localConversation
	}
	return Key{Terminal: m.watch.terminalID, Conversation: conversation}
}

func (m *Monitor) changeStateLocked(state string, cause Cause, request, now time.Time) bool {
	if driver, ok := cause.(LinkTurn); ok {
		if driver.Run == "" || driver.Seq == 0 || m.driver.Run != driver.Run || m.driver.Sequence >= driver.Seq {
			return false
		}
		m.driver.Sequence = driver.Seq
		if request.After(m.lastModelRequestAt) {
			m.lastModelRequestAt = request
		}
	}
	effects, _ := EffectsOf(cause)
	m.state = StateView{Sequence: m.state.Sequence + 1, Touch: effects.Touch, Broadcast: effects.Broadcast, Resolve: !Resolved(cause), State: protocol.SessionState(state), Since: now, Updated: now, Reason: m.reason}
	return true
}
