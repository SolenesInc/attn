package agentmailbox

type Kind string

const (
	KindGardenSeed        Kind = "garden_seed"
	KindPeerMessage       Kind = "peer_message"
	KindMaintenancePrompt Kind = "maintenance_prompt"
)

const (
	DefaultInboxLimit = 20
	MaxInboxLimit     = 50
)

type Item struct {
	ID                 string
	RecipientSessionID string
	Kind               Kind
	SourceID           string
	CoalesceKey        string
	Hint               string
	Prompt             string
	CreatedAt          string
	NotifiedAt         string
	ReadAt             string
}

type PeerMessage struct {
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
	Message            PeerMessage
	RecipientSessionID string
	NotifiedAt         string
	ReadAt             string
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
	Peer *PeerMessage
}

type PeerGuardCounts struct {
	DuplicateFromSender bool
	FromSenderInWindow  int
	UnreadForRecipient  int
}
