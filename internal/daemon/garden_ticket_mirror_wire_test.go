package daemon_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADispatchedSessionsSeedMovesMirrorOntoItsTicket(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("planner"), w.Path("peer"))
	planner, peer := panes[0].session, panes[1].session
	planted, err := cli.SeedPlant(planner, "Migrate the store to X", "the brief", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seed := planted.Seed.ID
	delegate := gardenSubscriptionDelegate(t, w, app, planner, "migrate", seed)

	board := gardenMirrorBoard(t, cli)
	gardenMirrorMove(t, cli, delegate, seed, "park", "")
	gardenMirrorMove(t, cli, delegate, seed, "tend", "")
	if after := gardenMirrorBoard(t, cli); !reflect.DeepEqual(after, board) {
		t.Errorf("moves by a dispatched session without a ticket changed the board to %+v", after)
	}

	createTicket(t, cli, planner, "Migrate the store to X", "migrate")
	if _, err := cli.TakeTicket(delegate, "migrate", false); err != nil {
		t.Fatalf("the delegate takes the ticket: %v", err)
	}
	gardenMirrorMove(t, cli, delegate, seed, "park", "")
	if status := showTicket(t, cli, "migrate").Status; status != protocol.TicketStatusBlocked {
		t.Errorf("parking left the ticket %s, want blocked", status)
	}
	gardenMirrorMove(t, cli, delegate, seed, "tend", "")
	if status := showTicket(t, cli, "migrate").Status; status != protocol.TicketStatusWorking {
		t.Errorf("tending again left the ticket %s, want working", status)
	}
	if _, err := cli.SeedNote(delegate, seed, "the parser landed and tests pass", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	noted := showTicket(t, cli, "migrate")
	if !gardenMirrorMentions(noted, "the parser landed and tests pass") {
		t.Errorf("the delegate's note is missing from the ticket thread %q", activityLines(noted))
	}
	if _, err := cli.SeedNote(peer, seed, "a peer chiming in", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if after := showTicket(t, cli, "migrate"); !reflect.DeepEqual(after.Activity, noted.Activity) {
		t.Errorf("a peer's note reached the ticket thread: %q", activityLines(after))
	}

	for _, move := range []struct {
		verb, reason string
		want         protocol.TicketStatus
	}{
		{"harvest", "what got done", protocol.TicketStatusDone},
		{"wither", "nobody should pick this up", protocol.TicketStatusFailed},
	} {
		ticketID := move.verb
		if move.verb == "wither" {
			gardenMirrorMove(t, cli, delegate, seed, "replant", "")
			gardenMirrorMove(t, cli, delegate, seed, "tend", "")
			createTicket(t, cli, planner, "Migrate the store to X again", ticketID)
			if _, err := cli.TakeTicket(delegate, ticketID, false); err != nil {
				t.Fatalf("the delegate takes %s: %v", ticketID, err)
			}
		} else {
			ticketID = "migrate"
		}
		gardenMirrorMove(t, cli, delegate, seed, move.verb, move.reason)
		ticket := showTicket(t, cli, ticketID)
		if ticket.Status != move.want || !gardenMirrorMentions(ticket, move.reason) {
			t.Errorf("after %s the ticket is %s with activity %q, want %s carrying the reason", move.verb, ticket.Status, activityLines(ticket), move.want)
		}
	}
}

func gardenMirrorBoard(t *testing.T, cli *client.Client) []protocol.Ticket {
	t.Helper()
	tickets, err := cli.TicketList("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return tickets
}

func gardenMirrorMentions(ticket *protocol.Ticket, text string) bool {
	return slices.ContainsFunc(ticket.Activity, func(a protocol.TicketActivity) bool { return protocol.Deref(a.Comment) == text })
}

func gardenMirrorMove(t *testing.T, cli *client.Client, session, seedID, verb, reason string) {
	t.Helper()
	if _, err := cli.SeedTransition(session, seedID, verb, reason, "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatalf("%s %s %s: %v", session, verb, seedID, err)
	}
}
