package automation

import (
	"testing"
	"time"
)

// The one mock-only fake AGENTS.md sanctions in this package, for
// ResolveContinuation three branches alone.
type fakeBindingStore struct {
	binding       *Binding
	ticketExists  bool
	released      bool
	releaseReason string
}

func (f *fakeBindingStore) GetActiveContinuityBinding(definitionID, continuityKey string) (*Binding, error) {
	return f.binding, nil
}
func (f *fakeBindingStore) ReleaseContinuityBinding(definitionID, continuityKey, reason string, now time.Time) error {
	f.released = true
	f.releaseReason = reason
	return nil
}
func (f *fakeBindingStore) TicketExists(ticketID string) (bool, error) {
	return f.ticketExists, nil
}

func TestResolveContinuationNoActiveBindingIsFresh(t *testing.T) {
	fake := &fakeBindingStore{binding: nil}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want Fresh with no binding", got)
	}
	if fake.released {
		t.Fatal("no binding to release, but ReleaseContinuityBinding was called")
	}
}

func TestResolveContinuationActiveBindingWithLiveTicketContinues(t *testing.T) {
	binding := &Binding{TicketID: "t1", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: true}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Fresh || got.Binding == nil || *got.Binding != *binding || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want continuation of %#v", got, binding)
	}
	if fake.released {
		t.Fatal("live ticket, but ReleaseContinuityBinding was called")
	}
}

func TestResolveContinuationOwnBindingIsFreshWithoutRelease(t *testing.T) {
	binding := &Binding{TicketID: "own-ticket", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: false}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want plain Fresh (no self-heal flag)", got)
	}
	if fake.released {
		t.Fatal("own not-yet-created ticket, but ReleaseContinuityBinding was called")
	}
}

func TestResolveContinuationDanglingBindingSelfHeals(t *testing.T) {
	binding := &Binding{TicketID: "t1", SessionID: "s1", WorkspaceID: "w1", PaneID: "p1"}
	fake := &fakeBindingStore{binding: binding, ticketExists: false}
	got, err := ResolveContinuation(fake, "def", "key", "own-ticket", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh || got.Binding != nil || !got.SelfHealedDanglingBinding {
		t.Fatalf("got %#v, want a self-healed Fresh delivery", got)
	}
	if !fake.released || fake.releaseReason != "ticket_swept" {
		t.Fatalf("release called=%v reason=%q, want ticket_swept", fake.released, fake.releaseReason)
	}
}
