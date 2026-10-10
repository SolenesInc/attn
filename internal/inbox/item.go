package inbox

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type Kind string

const (
	SeedUpdate  Kind = "garden_seed"
	PeerMessage Kind = "peer_message"
	Notice      Kind = "maintenance_prompt"
)

const (
	DefaultInboxLimit = 20
	MaxInboxLimit     = 50
)

type Item struct {
	ID     string
	To     who.Address
	Kind   Kind
	Text   string
	Hint   string
	Source string
	Key    string
}

type Message struct {
	ID        string
	Sender    who.Party
	Body      string
	CreatedAt string
}

type State string

const (
	StateQueued   State = "queued"
	StateNotified State = "notified"
	StateRead     State = "read"
)

type PeerRecord struct {
	To         who.Address
	Message    Message
	ReadBy     protocol.SessionID
	NotifiedAt string
	ReadAt     string
}

func (r PeerRecord) State() State {
	if r.ReadAt != "" {
		return StateRead
	}
	if r.NotifiedAt != "" {
		return StateNotified
	}
	return StateQueued
}

type PeerGuardCounts struct {
	DuplicateFromSender bool
	FromSenderInWindow  int
	UnreadForRecipient  int
}
