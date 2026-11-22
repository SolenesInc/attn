// Package harness is the contract between attn and the agent harnesses it hosts.
package harness

import (
	"context"
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

// Input is one delivery to a session. ID names the attempt; a harness may pass it on. Text is empty for a compact.
type Input struct {
	ID    string
	Text  string
	Voice Voice
	Ring  *protocol.InboxRingRecord
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
