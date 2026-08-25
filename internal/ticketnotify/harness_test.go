package ticketnotify

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

const ObserverChief = "chief"

func ChiefObserver() Observer {
	return Observer{ID: ObserverChief, AuthorID: ObserverChief, DeliveryID: ObserverChief}
}

type harness struct {
	t      *testing.T
	s      *store.Store
	now    time.Time
	nudges []string
}

func newHarness(t *testing.T) *harness {
	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	return &harness{t: t, s: s, now: time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)}
}

func (h *harness) tick() time.Time {
	h.now = h.now.Add(time.Minute)
	return h.now
}

func (h *harness) Nudge(observerID string) error {
	h.nudges = append(h.nudges, observerID)
	return nil
}

func hasComment(events []store.TicketEvent, comment string) bool {
	for _, e := range events {
		if e.Comment == comment {
			return true
		}
	}
	return false
}

func (h *harness) create(id, assignee, author string) {
	h.t.Helper()
	if _, err := h.s.CreateTicket(store.Ticket{ID: id, Title: id, Assignee: assignee}, author, h.tick()); err != nil {
		h.t.Fatalf("create %s: %v", id, err)
	}
}

func (h *harness) status(id string, to store.TicketStatus, author, comment string) {
	h.t.Helper()
	if _, err := h.s.SetTicketStatus(id, to, author, comment, h.tick()); err != nil {
		h.t.Fatalf("status %s: %v", id, err)
	}
}

func (h *harness) comment(id, author, comment string) {
	h.t.Helper()
	if _, err := h.s.AddTicketComment(id, author, comment, h.tick()); err != nil {
		h.t.Fatalf("comment %s: %v", id, err)
	}
}

func TestHarnessEmitConsumeCursor(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	h.status("alpha", store.TicketStatusWorking, "agent7", "on it")

	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("bundles = %+v, want one for alpha", bundles)
	}
	if len(bundles[0].Events) != 1 || bundles[0].Events[0].Kind != store.TicketEventStatusChanged {
		t.Fatalf("events = %+v, want one status_changed", bundles[0].Events)
	}
	if a := bundles[0].Events[0].Author; a == ObserverChief {
		t.Fatalf("chief consumed its own event (author=%s)", a)
	}

	if n, err := Unread(h.s, chief); err != nil || n != 0 {
		t.Fatalf("unread after consume = %d (err %v), want 0", n, err)
	}
	again, err := Consume(h.s, chief, h.tick())
	if err != nil || len(again) != 0 {
		t.Fatalf("second Consume = %+v (err %v), want empty", again, err)
	}

	h.status("alpha", store.TicketStatusInReview, "agent7", "ready for review")
	if n, _ := Unread(h.s, chief); n != 1 {
		t.Fatalf("unread after new event = %d, want 1", n)
	}
	final, _ := Consume(h.s, chief, h.tick())
	if len(final) != 1 || final[0].Events[0].ToStatus != store.TicketStatusInReview {
		t.Fatalf("final consume = %+v, want one in_review", final)
	}
}

func TestHarnessBundleByTicket(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	h.create("beta", "agent9", ObserverChief)

	h.status("alpha", store.TicketStatusWorking, "agent7", "")
	h.status("beta", store.TicketStatusWorking, "agent9", "")
	h.comment("alpha", "agent7", "halfway")

	bundles, err := Consume(h.s, chief, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 2 {
		t.Fatalf("bundles = %d, want 2 (one per ticket)", len(bundles))
	}
	if bundles[0].TicketID != "alpha" || bundles[1].TicketID != "beta" {
		t.Fatalf("bundle order = %s,%s, want alpha,beta", bundles[0].TicketID, bundles[1].TicketID)
	}
	if len(bundles[0].Events) != 2 {
		t.Fatalf("alpha events = %d, want 2", len(bundles[0].Events))
	}
	if len(bundles[1].Events) != 1 {
		t.Fatalf("beta events = %d, want 1", len(bundles[1].Events))
	}
}

func TestHarnessDedup(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()

	h.create("alpha", "agent7", ObserverChief)
	h.comment("alpha", "agent7", "ping")
	h.comment("alpha", "agent7", "ping")

	events, err := h.s.TicketEventsSince(0)
	if err != nil {
		t.Fatalf("TicketEventsSince: %v", err)
	}
	commentEvents := 0
	for _, e := range events {
		if e.Kind == store.TicketEventCommented {
			commentEvents++
		}
	}
	if commentEvents != 1 {
		t.Fatalf("comment events = %d, want 1 (deduped)", commentEvents)
	}

	if _, appended, err := h.s.AppendTicketEvent(store.TicketEvent{
		TicketID: "alpha", Kind: store.TicketEventCommented, Author: "agent7", Comment: "ping",
	}, h.tick()); err != nil || appended {
		t.Fatalf("re-append appended=%v (err %v), want false", appended, err)
	}

	bundles, _ := Consume(h.s, chief, h.tick())
	got := 0
	for _, b := range bundles {
		for _, e := range b.Events {
			if e.Kind == store.TicketEventCommented {
				got++
			}
		}
	}
	if got != 1 {
		t.Fatalf("chief saw %d comments, want 1", got)
	}
}

func TestHarnessNudgePath(t *testing.T) {
	h := newHarness(t)

	codex := Observer{ID: "codexbot"}
	claude := Observer{ID: "agent7"}

	h.create("alpha", "codexbot", ObserverChief)
	h.create("beta", "agent7", ObserverChief)

	h.comment("alpha", ObserverChief, "please rebase")
	h.comment("beta", ObserverChief, "tweak the title")

	d, err := Notify(h.s, codex, false, h, h.tick())
	if err != nil || d != DeliveryDeferred {
		t.Fatalf("approval-waiting codex delivery = %v (err %v), want Deferred", d, err)
	}
	if len(h.nudges) != 0 {
		t.Fatalf("nudges = %v, want none while waiting for approval", h.nudges)
	}

	d, err = Notify(h.s, codex, true, h, h.tick())
	if err != nil || d != DeliveryNudge {
		t.Fatalf("idle codex delivery = %v (err %v), want Nudge", d, err)
	}
	if len(h.nudges) != 1 || h.nudges[0] != "codexbot" {
		t.Fatalf("nudges = %v, want [codexbot]", h.nudges)
	}

	d, err = Notify(h.s, claude, true, h, h.tick())
	if err != nil || d != DeliveryNudge {
		t.Fatalf("claude delivery = %v (err %v), want Nudge", d, err)
	}
	if len(h.nudges) != 2 || h.nudges[1] != "agent7" {
		t.Fatalf("nudges = %v, want [codexbot agent7]", h.nudges)
	}

	bundles, _ := Consume(h.s, codex, h.tick())
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("codex consume = %+v, want only alpha", bundles)
	}
	if !hasComment(bundles[0].Events, "please rebase") {
		t.Fatalf("codex did not see the rebase steer: %+v", bundles[0].Events)
	}
	if d, _ := Notify(h.s, codex, true, h, h.tick()); d != DeliveryNone {
		t.Fatalf("re-notify after consume = %v, want None", d)
	}
}

func TestHarnessAgentScopeAndSelfAuthored(t *testing.T) {
	h := newHarness(t)
	chief := ChiefObserver()
	agent7 := Observer{ID: "agent7"}

	h.create("alpha", "agent7", ObserverChief)
	h.create("beta", "agent9", ObserverChief)

	h.status("alpha", store.TicketStatusWorking, "agent7", "starting")
	h.comment("alpha", ObserverChief, "one note")
	h.status("beta", store.TicketStatusWorking, "agent9", "starting")

	bundles, err := Consume(h.s, agent7, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "alpha" {
		t.Fatalf("agent7 bundles = %+v, want only alpha (never beta)", bundles)
	}
	for _, e := range bundles[0].Events {
		if e.Author == "agent7" {
			t.Fatalf("agent7 saw its own event: %+v", e)
		}
	}
	if !hasComment(bundles[0].Events, "one note") {
		t.Fatalf("agent7 missing the chief's note: %+v", bundles[0].Events)
	}

	chiefBundles, _ := Consume(h.s, chief, h.tick())
	tickets := map[string]bool{}
	for _, b := range chiefBundles {
		tickets[b.TicketID] = true
		for _, e := range b.Events {
			if e.Author == ObserverChief {
				t.Fatalf("chief saw its own event: %+v", e)
			}
		}
	}
	if !tickets["alpha"] || !tickets["beta"] {
		t.Fatalf("chief tickets = %v, want both alpha and beta", tickets)
	}
}

func TestHarnessLateAssignmentDeliversContext(t *testing.T) {
	h := newHarness(t)
	agent7 := Observer{ID: "agent7"}

	h.create("early", "agent7", ObserverChief)
	h.status("early", store.TicketStatusWorking, "agent7", "on early")
	if _, err := Consume(h.s, agent7, h.tick()); err != nil {
		t.Fatalf("consume early: %v", err)
	}

	h.create("late", "", ObserverChief)
	h.comment("late", ObserverChief, "pre-assignment brief")

	if n, err := Unread(h.s, agent7); err != nil || n != 0 {
		t.Fatalf("unread before assignment = %d (err %v), want 0", n, err)
	}

	if err := h.s.AssignTicket("late", "agent7", ObserverChief, h.tick()); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	bundles, err := Consume(h.s, agent7, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "late" {
		t.Fatalf("bundles = %+v, want only late", bundles)
	}
	if !hasComment(bundles[0].Events, "pre-assignment brief") {
		t.Fatalf("late did not deliver its pre-assignment brief: %+v", bundles[0].Events)
	}
	sawCreated := false
	for _, e := range bundles[0].Events {
		if e.Kind == store.TicketEventCreated {
			sawCreated = true
		}
	}
	if !sawCreated {
		t.Fatalf("late did not deliver its created event: %+v", bundles[0].Events)
	}
}

func TestHarnessReassignmentHandsOverHistory(t *testing.T) {
	h := newHarness(t)
	agent9 := Observer{ID: "agent9"}

	h.create("handoff", "agent7", ObserverChief)
	h.status("handoff", store.TicketStatusWorking, "agent7", "did the first half")

	if err := h.s.AssignTicket("handoff", "agent9", ObserverChief, h.tick()); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}

	bundles, err := Consume(h.s, agent9, h.tick())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(bundles) != 1 || bundles[0].TicketID != "handoff" {
		t.Fatalf("bundles = %+v, want only handoff", bundles)
	}
	kinds := map[store.TicketEventKind]bool{}
	for _, e := range bundles[0].Events {
		kinds[e.Kind] = true
	}
	if !kinds[store.TicketEventCreated] || !kinds[store.TicketEventStatusChanged] {
		t.Fatalf("agent9 did not inherit the full thread: %+v", bundles[0].Events)
	}
	if !hasComment(bundles[0].Events, "did the first half") {
		t.Fatalf("agent9 missing the prior progress note: %+v", bundles[0].Events)
	}
}
