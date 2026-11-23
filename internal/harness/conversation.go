// Package harness is the contract between attn and the agent harnesses it hosts.
package harness

import (
	"context"
	"errors"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// Voice says whose words an input carries.
type Voice uint8

const (
	// VoiceUser is the user's words: annotations and user conversation.
	VoiceUser Voice = iota
	// VoiceAttn is attn's own: rings, heartbeats, nudges, maintenance.
	VoiceAttn
)

// SessionFacts are what attn knows of a session's conversation when it asks the harness for its handle.
type SessionFacts struct {
	Session  SessionID
	Harness  ID
	Profile  string
	Lifetime Lifetime
	Terminal TerminalID

	Conversation, Path string
	Run, RunOwner      string
	Route              string
}

// Conversation is a session's conversation as its harness reaches it without the terminal.
type Conversation interface {
	Deliver(ctx context.Context, in Input) Custody
	Compact(ctx context.Context, in Input) Custody
	// CanCompact returns nil when Compact can run, or the harness's sentence for why it cannot.
	CanCompact() error
}

// TurnUse says how an input reaches a working session.
type TurnUse uint8

const (
	// Queue waits for the running turn to end: the harness's normal delivery.
	Queue TurnUse = iota
	// Steer joins the running turn at its next step without stopping it.
	Steer
	// Interrupt stops the running turn now and starts the next one with the input.
	Interrupt
)

// Input is one delivery to a session. ID names the attempt; a harness may pass it on. Text is empty for a compact.
type Input struct {
	ID    string
	Text  string
	Voice Voice
	Ring  *protocol.InboxRingRecord
	Turn  TurnUse
	// PendingApproval: the session waits on an approval or a question.
	PendingApproval bool
	// AskedInTurn: that wait followed working in this run.
	AskedInTurn bool
}

// Outcome is what the harness did with an input.
type Outcome uint8

const (
	// Deferred: not taken now, for Reason.
	Deferred Outcome = iota
	Taken
	// Held: taken, to be typed or returned later.
	Held
	// Rejected: refused for good, for Reason.
	Rejected
	// Stopped: the user stopped the session.
	Stopped
	// NotMine: not over the harness's channel, so attn types it into the terminal.
	NotMine
)

// Custody is a delivery's only result: what the harness did with the input, and when.
type Custody struct {
	Outcome Outcome
	At      time.Time
	Reason  string
	// Uncertain, with Deferred: the harness may have taken it.
	Uncertain bool
}

// TypedConversation is the conversation of a session attn types into: it takes no input itself, and compaction is refused with compactRefusal.
func TypedConversation(compactRefusal error) Conversation { return typedConversation{compactRefusal} }

type typedConversation struct{ refusal error }

func (typedConversation) Deliver(context.Context, Input) Custody { return Custody{Outcome: NotMine} }

func (typedConversation) Compact(context.Context, Input) Custody { return Custody{Outcome: NotMine} }

func (c typedConversation) CanCompact() error { return c.refusal }

// ResumedOrTerminal names the conversation a launch resumes, or else one named for its terminal.
func ResumedOrTerminal(req LaunchRequest) LaunchConversation {
	if req.Conversation != "" {
		return ConversationIs(req.Conversation)
	}
	return ConversationIs(string(req.Terminal))
}

// ErrTranscriptUnavailable is a transcript read of a conversation attn cannot find.
var ErrTranscriptUnavailable = errors.New("transcript unavailable")
