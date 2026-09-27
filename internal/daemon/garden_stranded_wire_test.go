package daemon_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStrandedTicketsAreReplantedAsWitheredSeedsOnce(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		registerSessions(t, w, cli, "worker")
		for _, ticket := range []struct{ id, title, brief string }{
			{"wire-the-thing", "Wire the thing", "the whole brief"},
			{"gave-up", "Gave up", "could not do it"},
			{"finished", "Finished", "already shipped"},
			{"auto-abcdef1234567890", "Looks automated", "but has no Automation provenance"},
		} {
			if _, err := cli.CreateTicket("planner", ticket.title, ticket.brief, ticket.id); err != nil {
				t.Fatalf("create %s: %v", ticket.id, err)
			}
			if _, err := cli.TakeTicket("worker", ticket.id, false); err != nil {
				t.Fatalf("worker takes %s: %v", ticket.id, err)
			}
		}
		inboxLines(t, cli, "worker")
		reportTicket(t, cli, "worker", "gave-up", protocol.DispatchWorkStateFailed, "could not")
		reportTicket(t, cli, "worker", "finished", protocol.DispatchWorkStateCompleted, "shipped")

		w.restart()
		w.advance(0)
		cli = w.Client()
		crashed := showTicket(t, cli, "wire-the-thing")
		if crashed.Status != protocol.TicketStatusCrashed {
			t.Fatalf("the dead worker's ticket is %s, want crashed", crashed.Status)
		}

		w.restart()
		w.advance(0)
		cli = w.Client()
		registerSessions(t, w, cli, "steady")
		createTicket(t, cli, "planner", "In flight", "in-flight")
		if _, err := cli.TakeTicket("steady", "in-flight", false); err != nil {
			t.Fatal(err)
		}
		inboxLines(t, cli, "steady")
		reportTicket(t, cli, "steady", "in-flight", protocol.DispatchWorkStateInProgress, "on it")
		seeds := gardenStrandedSeedsByTitle(t, cli)
		wired := seeds["Wire the thing"]
		if wired.Status != "withered" || wired.Body != "the whole brief" || wired.TenderSession != "" || wired.TenderMember != "" {
			t.Errorf("the crashed ticket's seed = %+v, want an unheld withered seed with the brief", wired)
		}
		notes, err := cli.SeedNotes("", wired.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(notes.Notes) != 1 || !strings.Contains(notes.Notes[0].Body, "wire-the-thing") || !strings.Contains(notes.Notes[0].Body, "worker") {
			t.Errorf("the replanted seed's log = %+v, want one note naming the ticket and the dead session", notes.Notes)
		}
		if gaveUp := seeds["Gave up"]; gaveUp.Status != "withered" || !strings.Contains(protocol.Deref(gaveUp.Reason), "gave-up") || gaveUp.TenderSession != "" {
			t.Errorf("the failed ticket's seed = %+v, want an unheld withered seed whose reason names the ticket", gaveUp)
		}
		if automated := seeds["Looks automated"]; automated.Status != "withered" {
			t.Errorf("the user ticket whose id looks automated became %+v, want a withered seed", automated)
		}
		if _, planted := seeds["Finished"]; planted {
			t.Error("a done ticket was replanted")
		}
		if after := showTicket(t, cli, "wire-the-thing"); !reflect.DeepEqual(after, crashed) {
			t.Errorf("replanting changed the ticket:\nbefore %+v\nafter  %+v", crashed, after)
		}

		w.restart()
		w.advance(0)
		cli = w.Client()
		if again := gardenStrandedSeedsByTitle(t, cli); len(again) != len(seeds) {
			t.Errorf("another restart left %d seeds, want the %d already planted", len(again), len(seeds))
		}
		for _, id := range []string{"finished", "in-flight"} {
			if ticket := ticketByID(t, cli, id); ticket.ArchivedAt != nil {
				t.Errorf("%s was archived: %+v", id, ticket)
			}
		}
		if err := w.InjectSession("worker", "worker", w.Path("worker"), protocol.SessionAgentClaude); err != nil {
			t.Fatal(err)
		}
		if ready, err := cli.SeedReady("", "", true); err != nil || len(ready.Seeds) != 0 {
			t.Errorf("with the dead session back the ready seeds are %+v (%v), want none", ready, err)
		}
	})
}

func TestATicketWhoseSessionDiedIsReplantedCarryingItsReconcileVerdict(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	w.finishStartupWork()
	app, cli := w.App(), w.Client()
	worker := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("store") })
	agent := w.Launched(worker)
	if _, err := cli.CreateTicket("planner", "Migrate the store", "Move the store onto the new backend.", "migrate-store"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.TakeTicket(worker, "migrate-store", false); err != nil {
		t.Fatal(err)
	}
	inboxLines(t, cli, worker)
	reportTicket(t, cli, worker, "migrate-store", protocol.DispatchWorkStateInProgress, "")
	app.TypeLine(worker, "migrate the store")
	agent.Prompted()
	testworld.AwaitSession(app, worker, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	agent.Exit(1)
	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "migrate-store") {
		t.Fatalf("the daemon asked %q, want the crashed ticket's reconciliation", task.Prompt)
	}
	if _, planted := gardenStrandedSeedsByTitle(t, cli)["Migrate the store"]; planted {
		t.Fatal("the crashed ticket was replanted before its verdict landed")
	}
	task.Answer(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"tests pass except e2e"}`)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool { return task.State == "done" })

	seed, planted := gardenStrandedSeedsByTitle(t, cli)["Migrate the store"]
	if !planted {
		t.Fatal("the reconciled crashed ticket was not replanted")
	}
	notes, err := cli.SeedNotes("", seed.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) != 1 || !strings.Contains(notes.Notes[0].Body, "What's left: e2e spec never ran") {
		t.Errorf("the replanted seed's log = %+v, want one note carrying the verdict", notes.Notes)
	}
	if ticket := showTicket(t, cli, "migrate-store"); ticket.Status != protocol.TicketStatusCrashed || ticket.ArchivedAt != nil {
		t.Errorf("replanting left the ticket %s archived at %v, want it crashed and on the board", ticket.Status, protocol.Deref(ticket.ArchivedAt))
	}
}

func gardenStrandedSeedsByTitle(t *testing.T, cli *client.Client) map[string]protocol.Seed {
	t.Helper()
	listed, err := cli.SeedList("", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]protocol.Seed{}
	for _, seed := range listed.Seeds {
		if _, twice := byTitle[seed.Title]; twice {
			t.Errorf("%q was planted twice", seed.Title)
		}
		byTitle[seed.Title] = seed
	}
	return byTitle
}
