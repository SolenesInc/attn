package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/sessioncost"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type RuntimeID string

type Key struct {
	Terminal     harness.TerminalID
	Runtime      RuntimeID
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
	ConversationGuessedAtLaunch
)

type Conversation struct {
	ConversationObservation
	Proposal ConversationProposal
	Terminal harness.TerminalID
}

type Exit struct {
	LifecycleID string
	Text        string
	Cols        int
	Rows        int
	Code        int
	Signal      string
	At          time.Time
}

type View struct {
	Conversation       Value[Conversation]                   `fact:"session.conversation.changed"`
	CostInitialized    Value[bool]                           `fact:"session.cost.changed"`
	Cost               KeyedSet[string, *sessioncost.Source] `fact:"session.cost.changed"`
	FirstReportAt      Value[time.Time]                      `fact:"-"`
	Key                Value[Key]                            `fact:"-"`
	Revision           Value[uint64]                         `fact:"-"`
	Succeeds           Value[Key]                            `fact:"-"`
	State              Value[StateView]                      `fact:"session.state.changed"`
	Turn               Value[TurnStamps]                     `fact:"session.state.changed"`
	AutoSettle         Value[AutoSettleView]                 `fact:"session.state.changed"`
	LastModelRequestAt Value[time.Time]                      `fact:"session.model_request.started"`
	Driver             Value[DriverMetadata]                 `fact:"session.state.changed"`
	Branch             Value[attngit.BranchInfo]             `fact:"session.branch.changed"`
	PullRequests       KeyedSet[string, PullRequest]         `fact:"session.pull_request.changed"`
	SentFiles          KeyedSet[string, uint64]              `fact:"-"`
	HarnessTitle       Value[string]                         `fact:"session.renamed"`
	LastSeen           Value[time.Time]                      `fact:"-"`
	Exit               Value[*Exit]                          `fact:"session.pty.exited"`
	Live               Value[bool]                           `fact:"-"`
	AssistantWindow    Value[uint64]                         `fact:"session.assistant_window.changed"`
}

func (e *Entry) viewLocked() View {
	e.revision++
	return e.snapshotLocked()
}

func (e *Entry) snapshotLocked() View {
	auto := AutoSettleView{DismissArmed: e.dismissal != autoSettleDismissalOff}
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
	return View{
		Conversation:       Value[Conversation]{e.conversationInput},
		Succeeds:           Value[Key]{e.succeeds},
		CostInitialized:    Value[bool]{e.costInitialized},
		Cost:               KeyedSet[string, *sessioncost.Source]{Entries: e.cost},
		Key:                Value[Key]{e.keyLocked()},
		Revision:           Value[uint64]{e.revision},
		State:              Value[StateView]{state},
		Turn:               Value[TurnStamps]{e.turn},
		AutoSettle:         Value[AutoSettleView]{auto},
		LastModelRequestAt: Value[time.Time]{e.lastModelRequestAt},
		Driver:             Value[DriverMetadata]{e.driver},
		FirstReportAt:      Value[time.Time]{e.firstReportAt},
		Branch:             Value[attngit.BranchInfo]{e.branch},
		PullRequests:       KeyedSet[string, PullRequest]{Entries: e.pullRequests},
		SentFiles:          KeyedSet[string, uint64]{Entries: e.sentFiles},
		HarnessTitle:       Value[string]{e.harnessTitle},
		LastSeen:           Value[time.Time]{e.touchedAt},
		Exit:               Value[*Exit]{e.exit},
		Live:               Value[bool]{live},
		AssistantWindow:    Value[uint64]{e.windowRevision},
	}
}

func (e *Entry) keyLocked() Key {
	conversation := e.conversation
	if conversation == "" {
		conversation = e.localConversation
	}
	return Key{Terminal: e.monitor.terminalID, Runtime: e.monitor.runtime, Conversation: conversation}
}

func (e *Entry) changeStateLocked(state string, cause Cause, request, now time.Time) bool {
	if driver, ok := cause.(LinkTurn); ok {
		if driver.Run == "" || driver.Seq == 0 || e.driver.Run != driver.Run || e.driver.Sequence >= driver.Seq {
			return false
		}
		e.driver.Sequence = driver.Seq
		if request.After(e.lastModelRequestAt) {
			e.lastModelRequestAt = request
		}
	}
	effects, _ := EffectsOf(cause)
	e.state = StateView{Sequence: e.state.Sequence + 1, Touch: effects.Touch, Broadcast: effects.Broadcast, Resolve: !Resolved(cause), State: protocol.SessionState(state), Since: now, Updated: now, Reason: e.reason}
	return true
}
