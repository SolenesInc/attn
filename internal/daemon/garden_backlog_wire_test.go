package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTheGardenCutoverPlantsUnassignedTodosOnce(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		registerSessions(t, w, cli, "worker")
		for _, ticket := range []struct{ id, title, brief string }{
			{"wire-the-thing", "Wire the thing", "the whole brief"},
			{"held-todo", "Held todo", "somebody has this"},
			{"finished", "Finished", "already shipped"},
			{"in-flight", "In flight", "being worked"},
		} {
			if _, err := cli.CreateTicket("planner", ticket.title, ticket.brief, ticket.id); err != nil {
				t.Fatalf("create %s: %v", ticket.id, err)
			}
		}
		for _, id := range []string{"held-todo", "finished", "in-flight"} {
			if _, err := cli.TakeTicket("worker", id, false); err != nil {
				t.Fatalf("worker takes %s: %v", id, err)
			}
		}
		inboxLines(t, cli, "worker")
		reportTicket(t, cli, "worker", "finished", protocol.DispatchWorkStateCompleted, "shipped")
		reportTicket(t, cli, "worker", "in-flight", protocol.DispatchWorkStateInProgress, "on it")

		w.restart()
		w.advance(0)
		cli = w.Client()
		seeds := gardenStrandedSeedsByTitle(t, cli)
		converted, ok := seeds["Wire the thing"]
		if !ok || len(seeds) != 1 {
			t.Fatalf("the cutover planted %+v, want only the unassigned todo", seeds)
		}
		if converted.Status != "planted" || converted.Body != "the whole brief" || converted.TenderSession != "" {
			t.Errorf("the converted seed = %+v, want it planted with the brief and no tender", converted)
		}
		if notes, err := cli.SeedNotes("", converted.ID, 0); err != nil || len(notes.Notes) != 1 || !strings.Contains(notes.Notes[0].Body, "wire-the-thing") {
			t.Errorf("the converted seed's log = %+v (%v), want one note naming the ticket", notes, err)
		}
		if ticket := ticketByID(t, cli, "wire-the-thing"); ticket.ArchivedAt == nil || ticket.Description != "the whole brief" {
			t.Errorf("the converted ticket = %+v, want it archived with its record intact", ticket)
		}
		for _, id := range []string{"held-todo", "finished", "in-flight"} {
			if ticket := ticketByID(t, cli, id); ticket.ArchivedAt != nil {
				t.Errorf("the cutover archived %s: %+v", id, ticket)
			}
		}

		w.restart()
		w.advance(0)
		if again, ok := gardenStrandedSeedsByTitle(t, w.Client())["Wire the thing"]; !ok || again.ID != converted.ID {
			t.Errorf("after another restart the todo's seed is %+v, want %s alone", again, converted.ID)
		}
	})
}
