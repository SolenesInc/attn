package sessionmonitor

import (
	"context"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

type Home interface {
	Applied(View) bool
	SnoozeWoke(at time.Time, cause string)

	PrimaryTerminal() harness.TerminalID
	Input(ctx context.Context, terminal harness.TerminalID, data []byte) error
	Link(voice harness.Voice) harness.Link
	NoteKey(terminal harness.TerminalID, at time.Time)
	LastKey() time.Time

	EnqueueTitle(conversation, source string) error
	Conversation() ConversationObservation
	ConversationClaimedElsewhere(nativeID string) bool
	LastSeen() string
	LinkTranscriptPath(path string) string
	AssistantWindowChanged()
}

type Policy struct {
	ChiefOfStaff       bool
	AutoSettle         AutoSettleSettings
	Headless           bool
	TitlesAllowed      bool
	DriverReportsState bool
	QuietNotifications bool
}

type Launch struct {
	Known              bool
	Agent              protocol.SessionAgent
	StartedAt          time.Time
	Directory          string
	Branch             attngit.BranchInfo
	HarnessTitle       string
	State              protocol.SessionState
	StateSince         time.Time
	StateUpdated       time.Time
	LastModelRequestAt time.Time
	Turn               TurnStamps
	Driver             DriverMetadata
	Cost               sessioncost.Checkpoint
	Exit               *Exit
	Conversation       ConversationObservation
}

func (e *Entry) SetPolicy(policy Policy) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.policy = policy
	e.mu.Unlock()
}

func (e *Entry) Policy() Policy {
	if e == nil {
		return Policy{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.policy
}

func (e *Entry) Bind(label string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.label = label
	e.mu.Unlock()
}

func (e *Entry) Label() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.label
}

func (e *Entry) logf(format string, args ...any) { e.registry.host.Logf(format, args...) }

func (e *Entry) current() bool {
	e.registry.mu.Lock()
	defer e.registry.mu.Unlock()
	_, ok := e.registry.entries[e]
	return ok
}

func (e *Entry) Launched(at time.Time) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.startedAt = at
	e.mu.Unlock()
}
