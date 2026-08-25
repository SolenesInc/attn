package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func bindMirrorFixture(t *testing.T, d *Daemon, sessionID string) (seedID, ticketID string) {
	t.Helper()
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Migrate the store to X", Body: protocol.Ptr("the brief")})
	if err := d.recordGardenDispatch(sessionID, seed.ID, "", "/tmp/a", "codex", false); err != nil {
		t.Fatalf("record dispatch: %v", err)
	}
	created, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:    "Migrate the store to X",
		Status:   store.TicketStatusWorking,
		Assignee: sessionID,
	}, "migrate-the-store-to-x", "chief", store.TicketRoleChiefOfStaff, nil, time.Now())
	if err != nil {
		t.Fatalf("bind legacy ticket: %v", err)
	}
	return seed.ID, created.ID
}

func TestSeedMoveMirrorsOntoTheBoundTicket(t *testing.T) {
	for _, tc := range []struct {
		verb   garden.Verb
		reason string
		want   store.TicketStatus
	}{
		{garden.VerbPark, "", store.TicketStatusBlocked},
		{garden.VerbHarvest, "what got done", store.TicketStatusDone},
		{garden.VerbWither, "nobody should pick this up", store.TicketStatusFailed},
	} {
		t.Run(string(tc.verb), func(t *testing.T) {
			d := newGardenDaemon(t)
			seedID, ticketID := bindMirrorFixture(t, d, "sess-a")
			move(t, d, "sess-a", seedID, garden.VerbTend, "", "")
			move(t, d, "sess-a", seedID, tc.verb, tc.reason, "")

			ticket, err := d.store.GetTicket(ticketID)
			if err != nil {
				t.Fatalf("GetTicket: %v", err)
			}
			if ticket.Status != tc.want {
				t.Fatalf("ticket status after %s = %q, want %q", tc.verb, ticket.Status, tc.want)
			}
			if tc.reason != "" && !mentionsComment(ticket, tc.reason) {
				t.Fatalf("ticket activity does not carry the reason: %+v", ticket.Activity)
			}
		})
	}
}

func TestTendingMirrorsTheTicketBackToWorking(t *testing.T) {
	d := newGardenDaemon(t)
	seedID, ticketID := bindMirrorFixture(t, d, "sess-a")
	move(t, d, "sess-a", seedID, garden.VerbTend, "", "")
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if ticket.Status != store.TicketStatusWorking {
		t.Fatalf("ticket status after tend = %q, want working", ticket.Status)
	}
}

func TestSeedNoteMirrorsOntoTheBoundTicket(t *testing.T) {
	d := newGardenDaemon(t)
	seedID, ticketID := bindMirrorFixture(t, d, "sess-a")
	move(t, d, "sess-a", seedID, garden.VerbTend, "", "")
	note(t, d, "sess-a", seedID, "the parser landed and tests pass", "")

	ticket, err := d.store.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if !mentionsComment(ticket, "the parser landed and tests pass") {
		t.Fatalf("ticket activity does not carry the note: %+v", ticket.Activity)
	}
}

func TestMirrorIsSilentWithoutTheSessionsOwnTicket(t *testing.T) {
	t.Run("no ticket", func(t *testing.T) {
		d := newGardenDaemon(t)
		seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Post-cutover work", Body: protocol.Ptr("the brief")})
		if err := d.recordGardenDispatch("sess-a", seed.ID, "", "/tmp/a", "codex", false); err != nil {
			t.Fatalf("record dispatch: %v", err)
		}
		move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
		move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "done", "")
		tickets, err := d.store.ListTickets(store.TicketListFilter{IncludeArchived: true})
		if err != nil {
			t.Fatalf("ListTickets: %v", err)
		}
		if len(tickets) != 0 {
			t.Fatalf("a post-cutover delegation touched the board: %+v", tickets)
		}
	})

	t.Run("peer", func(t *testing.T) {
		d := newGardenDaemon(t)
		addGardenSession(t, d, "sess-peer")
		seedID, ticketID := bindMirrorFixture(t, d, "sess-a")
		move(t, d, "sess-a", seedID, garden.VerbTend, "", "")
		before, err := d.store.GetTicket(ticketID)
		if err != nil {
			t.Fatalf("GetTicket: %v", err)
		}
		note(t, d, "sess-peer", seedID, "a peer chiming in", "")
		after, err := d.store.GetTicket(ticketID)
		if err != nil {
			t.Fatalf("GetTicket: %v", err)
		}
		if len(after.Activity) != len(before.Activity) {
			t.Fatalf("a peer's note reached the ticket thread: %+v", after.Activity)
		}
	})
}

func TestEverySeedMoveHasATicketColumn(t *testing.T) {
	for _, verb := range garden.Verbs {
		if _, ok := seedMoveTicketStatus(verb); !ok {
			t.Errorf("seedMoveTicketStatus(%q) has no column", verb)
		}
	}
}

func mentionsComment(ticket *store.Ticket, text string) bool {
	for _, activity := range ticket.Activity {
		if strings.Contains(activity.Comment, text) {
			return true
		}
	}
	return false
}
