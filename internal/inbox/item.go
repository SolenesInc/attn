package inbox

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
	ID          string
	To          Address
	Text        string
	Source      string
	Key         string
	Attempts    int
	AttemptedAt string
	ReadBy      string
	Kind        Kind
	BellName    string
	Hint        string
	CreatedAt   string
	NotifiedAt  string
	ReadAt      string
}

type Message struct {
	ID              string
	SenderSessionID string
	Body            string
	CreatedAt       string
}

type State string

const (
	StateQueued   State = "queued"
	StateNotified State = "notified"
	StateRead     State = "read"
)

type PeerRecord struct {
	To         Address
	Message    Message
	ReadBy     string
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

type Delivery struct {
	Item Item
	Peer *Message
}

type PeerGuardCounts struct {
	DuplicateFromSender bool
	FromSenderInWindow  int
	UnreadForRecipient  int
}
