package automation

import "time"

// Duplicated because this package cannot import internal/store (see BindingStore).
const ticketSweptReleaseReason = "ticket_swept"

type Binding struct {
	TicketID, SessionID, WorkspaceID, PaneID string
}

type Continuation struct {
	Fresh                     bool
	Binding                   *Binding
	SelfHealedDanglingBinding bool
}

// internal/automation never imports internal/store or internal/daemon.
type BindingStore interface {
	GetActiveContinuityBinding(definitionID, continuityKey string) (*Binding, error)
	ReleaseContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error
	TicketExists(ticketID string) (bool, error)
}

// ownTicketID is the run's claim-time reserved ticket ID, known before the ticket exists:
// a binding pointing at it is being born, not dangling, and releasing it breaks continuity.
func ResolveContinuation(s BindingStore, definitionID, continuityKey, ownTicketID string, now time.Time) (Continuation, error) {
	binding, err := s.GetActiveContinuityBinding(definitionID, continuityKey)
	if err != nil {
		return Continuation{}, err
	}
	if binding == nil {
		return Continuation{Fresh: true}, nil
	}
	if binding.TicketID == ownTicketID {
		return Continuation{Fresh: true}, nil
	}
	exists, err := s.TicketExists(binding.TicketID)
	if err != nil {
		return Continuation{}, err
	}
	if exists {
		return Continuation{Binding: binding}, nil
	}
	if err := s.ReleaseContinuityBinding(definitionID, continuityKey, ticketSweptReleaseReason, now); err != nil {
		return Continuation{}, err
	}
	return Continuation{Fresh: true, SelfHealedDanglingBinding: true}, nil
}
