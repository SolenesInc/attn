package ticketnotify

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

const (
	modelWorker    = "worker-session"
	modelDelegator = "delegator-session"
	modelBystander = "bystander-session"
)

var modelChiefRole = store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)

type routingModel struct {
	assignee   map[string]string
	roleOwners map[string]map[string]bool
	subs       map[string]map[string]bool
	cursors    map[string]map[string]int64
	events     []store.TicketEvent
}

func newRoutingModel() *routingModel {
	return &routingModel{
		assignee:   map[string]string{},
		roleOwners: map[string]map[string]bool{},
		subs:       map[string]map[string]bool{},
		cursors:    map[string]map[string]int64{},
	}
}

func (m *routingModel) participates(identity, ticket string) bool {
	if identity == "" || ticket == "" {
		return false
	}
	if m.assignee[ticket] == identity {
		return true
	}
	for role := range m.roleOwners[ticket] {
		if identity == store.TicketRoleIdentity(role) {
			return true
		}
	}
	if m.subs[ticket][identity] {
		return true
	}
	for _, e := range m.events {
		if e.TicketID != ticket || e.Author != identity || e.Kind == store.TicketEventCommented {
			continue
		}
		if e.Kind == store.TicketEventCreated && len(m.roleOwners[ticket]) > 0 {
			continue
		}
		return true
	}
	return false
}

func (m *routingModel) participants(ticket string) []string {
	seen := map[string]bool{}
	if a := m.assignee[ticket]; a != "" {
		seen[a] = true
	}
	for role := range m.roleOwners[ticket] {
		seen[store.TicketRoleIdentity(role)] = true
	}
	for identity := range m.subs[ticket] {
		seen[identity] = true
	}
	for _, e := range m.events {
		if e.TicketID != ticket || e.Author == "" || e.Kind == store.TicketEventCommented {
			continue
		}
		if e.Kind == store.TicketEventCreated && len(m.roleOwners[ticket]) > 0 {
			continue
		}
		seen[e.Author] = true
	}
	out := make([]string, 0, len(seen))
	for identity := range seen {
		out = append(out, identity)
	}
	sort.Strings(out)
	return out
}

func (m *routingModel) cursor(identity, ticket string) int64 {
	return m.cursors[identity][ticket]
}

func (m *routingModel) setCursor(identity, ticket string, seq int64) {
	if m.cursors[identity] == nil {
		m.cursors[identity] = map[string]int64{}
	}
	if seq > m.cursors[identity][ticket] {
		m.cursors[identity][ticket] = seq
	}
}

func (m *routingModel) unread(cursorID, authorID string) []int64 {
	var seqs []int64
	for _, e := range m.events {
		if e.Author == authorID || e.Seq <= m.cursor(cursorID, e.TicketID) {
			continue
		}
		if !m.participates(cursorID, e.TicketID) {
			continue
		}
		seqs = append(seqs, e.Seq)
	}
	slices.Sort(seqs)
	return seqs
}

func (m *routingModel) consumeAll(observers []Observer) []int64 {
	seen := map[int64]bool{}
	for _, obs := range observers {
		authorID := obs.AuthorID
		if authorID == "" {
			authorID = obs.ID
		}
		advance := map[string]int64{}
		for _, seq := range m.unread(obs.ID, authorID) {
			seen[seq] = true
			e := m.eventBySeq(seq)
			if seq > advance[e.TicketID] {
				advance[e.TicketID] = seq
			}
		}
		for ticket, seq := range advance {
			m.setCursor(obs.ID, ticket, seq)
		}
	}
	out := make([]int64, 0, len(seen))
	for seq := range seen {
		out = append(out, seq)
	}
	slices.Sort(out)
	return out
}

func (m *routingModel) eventBySeq(seq int64) store.TicketEvent {
	for _, e := range m.events {
		if e.Seq == seq {
			return e
		}
	}
	return store.TicketEvent{}
}

type world struct {
	t            *testing.T
	s            *store.Store
	m            *routingModel
	now          time.Time
	tickets      []string
	chiefSession string
}

func newWorld(t *testing.T) *world {
	s := store.New()
	t.Cleanup(func() { _ = s.Close() })
	return &world{
		t:            t,
		s:            s,
		m:            newRoutingModel(),
		now:          time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC),
		chiefSession: "chief-session-1",
	}
}

func (w *world) tick() time.Time {
	w.now = w.now.Add(time.Minute)
	return w.now
}

func (w *world) syncEvents() {
	w.t.Helper()
	events, err := w.s.TicketEventsSince(0)
	if err != nil {
		w.t.Fatalf("TicketEventsSince: %v", err)
	}
	w.m.events = events
}

func (w *world) chiefObservers() []Observer {
	return []Observer{
		{ID: w.chiefSession, AuthorID: w.chiefSession, DeliveryID: w.chiefSession},
		{ID: modelChiefRole, AuthorID: w.chiefSession, DeliveryID: w.chiefSession},
	}
}

func (w *world) observerSets() map[string][]Observer {
	return map[string][]Observer{
		"chief":     w.chiefObservers(),
		"worker":    {{ID: modelWorker}},
		"delegator": {{ID: modelDelegator}},
		"bystander": {{ID: modelBystander}},
	}
}

func (w *world) createDelegation(id, assignee, author, ownerRole string, subscribers []string) {
	w.t.Helper()
	if _, err := w.s.CreateTicketWithSubscribers(
		store.Ticket{ID: id, Title: id, Assignee: assignee}, author, ownerRole, subscribers, w.tick(),
	); err != nil {
		w.t.Fatalf("create %s: %v", id, err)
	}
	w.tickets = append(w.tickets, id)
	w.m.assignee[id] = assignee
	if ownerRole != "" {
		w.m.roleOwners[id] = map[string]bool{ownerRole: true}
	}
	w.m.subs[id] = map[string]bool{}
	for _, identity := range subscribers {
		if strings.TrimSpace(identity) != "" {
			w.m.subs[id][identity] = true
		}
	}
	w.syncEvents()
	if assignee != "" {
		for _, e := range w.m.events {
			if e.TicketID == id && e.Kind == store.TicketEventCreated {
				w.m.setCursor(assignee, id, e.Seq)
			}
		}
	}
}

func (w *world) assign(ticket, assignee, author string) {
	w.t.Helper()
	if err := w.s.AssignTicket(ticket, assignee, author, w.tick()); err != nil {
		w.t.Fatalf("assign %s: %v", ticket, err)
	}
	w.m.assignee[ticket] = assignee
	w.syncEvents()
}

func (w *world) status(ticket string, to store.TicketStatus, author, comment string) {
	w.t.Helper()
	if _, err := w.s.SetTicketStatus(ticket, to, author, comment, w.tick()); err != nil {
		w.t.Fatalf("status %s: %v", ticket, err)
	}
	w.syncEvents()
}

func (w *world) comment(ticket, author, text string) {
	w.t.Helper()
	if _, err := w.s.AddTicketComment(ticket, author, text, w.tick()); err != nil {
		w.t.Fatalf("comment %s: %v", ticket, err)
	}
	w.syncEvents()
}

func (w *world) editDescription(ticket, description, author string) {
	w.t.Helper()
	if err := w.s.EditTicketDescription(ticket, description, author, w.tick()); err != nil {
		w.t.Fatalf("edit %s: %v", ticket, err)
	}
	w.syncEvents()
}

func (w *world) subscribe(identity, ticket string) {
	w.t.Helper()
	if err := w.s.AddTicketSubscription(identity, ticket, w.tick()); err != nil {
		w.t.Fatalf("subscribe %s to %s: %v", identity, ticket, err)
	}
	if w.m.subs[ticket] == nil {
		w.m.subs[ticket] = map[string]bool{}
	}
	w.m.subs[ticket][identity] = true
}

func (w *world) unsubscribe(identity, ticket string) {
	w.t.Helper()
	if err := w.s.RemoveTicketSubscription(identity, ticket); err != nil {
		w.t.Fatalf("unsubscribe %s from %s: %v", identity, ticket, err)
	}
	delete(w.m.subs[ticket], identity)
}

func (w *world) checkParticipants(step string) {
	w.t.Helper()
	for _, ticket := range w.tickets {
		got, err := w.s.TicketParticipants(ticket)
		if err != nil {
			w.t.Fatalf("%s: TicketParticipants(%s): %v", step, ticket, err)
		}
		want := w.m.participants(ticket)
		if !slices.Equal(got, want) {
			w.t.Fatalf("%s: participants(%s) = %v, model says %v", step, ticket, got, want)
		}
	}
}

func (w *world) checkUnread(step string) {
	w.t.Helper()
	for name, observers := range w.observerSets() {
		var got []int64
		for _, obs := range observers {
			authorID := obs.AuthorID
			if authorID == "" {
				authorID = obs.ID
			}
			events, err := w.s.UnreadTicketEventsFor(obs.ID, authorID)
			if err != nil {
				w.t.Fatalf("%s: UnreadTicketEventsFor(%s): %v", step, obs.ID, err)
			}
			for _, e := range events {
				got = append(got, e.Seq)
			}
		}
		got = dedupSorted(got)

		var want []int64
		for _, obs := range observers {
			authorID := obs.AuthorID
			if authorID == "" {
				authorID = obs.ID
			}
			want = append(want, w.m.unread(obs.ID, authorID)...)
		}
		want = dedupSorted(want)

		if !slices.Equal(got, want) {
			w.t.Fatalf("%s: unread for %s = %v, model says %v", step, name, got, want)
		}
	}
}

func (w *world) consumeAndCheck(step, name string) {
	w.t.Helper()
	observers := w.observerSets()[name]
	bundles, err := ConsumeAll(w.s, observers, w.tick())
	if err != nil {
		w.t.Fatalf("%s: ConsumeAll(%s): %v", step, name, err)
	}
	var got []int64
	for _, b := range bundles {
		for _, e := range b.Events {
			got = append(got, e.Seq)
			if e.TicketID != b.TicketID {
				w.t.Fatalf("%s: bundle %s carries an event for %s", step, b.TicketID, e.TicketID)
			}
		}
	}
	slices.Sort(got)
	want := w.m.consumeAll(observers)
	if !slices.Equal(got, want) {
		w.t.Fatalf("%s: %s consumed %v, model says %v", step, name, got, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			w.t.Fatalf("%s: %s was delivered seq %d twice", step, name, got[i])
		}
	}
}

func TestRoutingMatchesModel(t *testing.T) {
	w := newWorld(t)
	rng := rand.New(rand.NewSource(20260729))

	actors := []string{modelWorker, modelDelegator, modelBystander, w.chiefSession}
	statuses := []store.TicketStatus{
		store.TicketStatusTodo,
		store.TicketStatusWorking,
		store.TicketStatusInReview,
		store.TicketStatusBlocked,
	}
	setNames := []string{"chief", "worker", "delegator", "bystander"}

	w.createDelegation("seed", modelWorker, w.chiefSession, store.TicketRoleChiefOfStaff, []string{modelDelegator})

	const steps = 400
	for i := range steps {
		step := fmt.Sprintf("step %d", i)
		ticket := w.tickets[rng.Intn(len(w.tickets))]
		actor := actors[rng.Intn(len(actors))]

		switch rng.Intn(10) {
		case 0:
			id := fmt.Sprintf("deleg-%d", i)
			w.createDelegation(id, modelWorker, w.chiefSession, store.TicketRoleChiefOfStaff, []string{modelDelegator})
		case 1:
			id := fmt.Sprintf("backlog-%d", i)
			w.createDelegation(id, "", actor, "", nil)
		case 2:
			w.status(ticket, statuses[rng.Intn(len(statuses))], actor, fmt.Sprintf("note %d", i))
		case 3:
			w.comment(ticket, actor, fmt.Sprintf("comment %d", i))
		case 4:
			w.assign(ticket, actors[rng.Intn(len(actors))], actor)
		case 5:
			w.editDescription(ticket, fmt.Sprintf("brief %d", i), actor)
		case 6:
			w.subscribe(actor, ticket)
		case 7:
			w.unsubscribe(actor, ticket)
		case 8:
			w.chiefSession = fmt.Sprintf("chief-session-%d", i)
			actors[3] = w.chiefSession
		case 9:
			w.consumeAndCheck(step, setNames[rng.Intn(len(setNames))])
		}

		w.checkParticipants(step)
		w.checkUnread(step)
	}

	for _, name := range setNames {
		w.consumeAndCheck("drain", name)
	}
	for _, name := range setNames {
		for _, obs := range w.observerSets()[name] {
			n, err := Unread(w.s, obs)
			if err != nil {
				t.Fatalf("Unread(%s): %v", obs.ID, err)
			}
			if n != 0 {
				t.Fatalf("%s (%s) still has %d unread after a full drain", name, obs.ID, n)
			}
		}
	}
}

func TestRoutingCommenterIsNotEnrolled(t *testing.T) {
	w := newWorld(t)
	w.createDelegation("alpha", modelWorker, w.chiefSession, store.TicketRoleChiefOfStaff, []string{modelDelegator})

	w.comment("alpha", modelBystander, "drive-by note")
	w.checkParticipants("after drive-by comment")
	if got, _ := w.s.TicketParticipants("alpha"); slices.Contains(got, modelBystander) {
		t.Fatalf("a one-shot commenter was enrolled: %v", got)
	}

	w.status("alpha", store.TicketStatusInReview, modelWorker, "ready")
	if n, err := Unread(w.s, Observer{ID: modelBystander}); err != nil || n != 0 {
		t.Fatalf("bystander unread = %d (err %v), want 0", n, err)
	}

	w.subscribe(modelBystander, "alpha")
	w.checkParticipants("after subscribe")
	n, err := Unread(w.s, Observer{ID: modelBystander})
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if n == 0 {
		t.Fatalf("subscribing delivered no backlog")
	}
}

func TestRoutingRoleTransferKeepsCursor(t *testing.T) {
	w := newWorld(t)
	w.createDelegation("alpha", modelWorker, w.chiefSession, store.TicketRoleChiefOfStaff, []string{modelDelegator})
	w.status("alpha", store.TicketStatusWorking, modelWorker, "starting")

	w.consumeAndCheck("first chief", "chief")

	w.chiefSession = "chief-session-2"

	if n, err := UnreadAny(w.s, w.chiefObservers()); err != nil || n != 0 {
		t.Fatalf("new chief session unread = %d (err %v), want 0 — history was replayed", n, err)
	}

	w.status("alpha", store.TicketStatusInReview, modelWorker, "ready")
	w.checkUnread("after role transfer")
	w.consumeAndCheck("second chief", "chief")
}

func TestRoutingDelegatorHearsWithoutOwningTheTicket(t *testing.T) {
	w := newWorld(t)
	w.createDelegation("alpha", modelWorker, modelDelegator, "", []string{modelDelegator, modelChiefRole})

	owned, err := w.s.IsTicketRoleOwner(store.TicketRoleChiefOfStaff, "alpha")
	if err != nil {
		t.Fatalf("IsTicketRoleOwner: %v", err)
	}
	if owned {
		t.Fatalf("an ordinary delegation took durable chief-role ownership")
	}

	w.status("alpha", store.TicketStatusInReview, modelWorker, "ready for review")
	w.checkParticipants("ordinary delegation")
	w.checkUnread("ordinary delegation")

	for _, name := range []string{"delegator", "chief"} {
		w.consumeAndCheck("ordinary delegation", name)
	}
}

func dedupSorted(seqs []int64) []int64 {
	slices.Sort(seqs)
	return slices.Compact(seqs)
}
