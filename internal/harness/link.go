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

// Input is one delivery to a session. ID names the attempt; a link may pass it on.
type Input struct {
	ExpectedDriverRun    string
	ExpectedConversation string
	Session              protocol.SessionID
	ID                   string
	Text                 string
	Voice                Voice
	Ring                 *protocol.InboxRingRecord
}

// Custody is a delivery's only result: whether the harness took the input, and when.
type Custody struct {
	Uncertain bool
	Rejected  bool
	Taken     bool
	At        time.Time
	Reason    string
	Stopped   bool
	Held      bool
}

// Link delivers input to a harness without going through its terminal.
type Link interface {
	Deliver(ctx context.Context, in Input) Custody
}

// QuestionSafeLink is a Link whose delivery cannot answer a permission question. It says whether it may deliver while
// the session waits on one, given whether attn saw the question come from a running turn.
type QuestionSafeLink interface {
	Link
	SafeDuringQuestion(askedInTurn bool) bool
}

// Compactor is a Link that can compact the conversation it delivers to, in the harness's own way.
type Compactor interface {
	Link
	Compact(ctx context.Context, in Input) Custody
}
