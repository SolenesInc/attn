package agentmailbox

type Kind string

const (
	KindGardenSeed        Kind = "garden_seed"
	KindPeerMessage       Kind = "peer_message"
	KindMaintenancePrompt Kind = "maintenance_prompt"
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

type State string

const (
	StateQueued   State = "queued"
	StateNotified State = "notified"
	StateRead     State = "read"
)

func (i Item) State() State {
	if i.ReadAt != "" {
		return StateRead
	}
	if i.NotifiedAt != "" {
		return StateNotified
	}
	return StateQueued
}

type PeerMessage struct {
	ID              string
	SenderSessionID string
	Body            string
	CreatedAt       string
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
