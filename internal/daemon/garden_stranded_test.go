package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

func seedBacklogTicket(t *testing.T, d *Daemon, id, title, description string, status store.TicketStatus, assignee string) {
	t.Helper()
	if _, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title:       title,
		Description: description,
		Status:      status,
		Assignee:    assignee,
	}, id, "chief", store.TicketRoleChiefOfStaff, nil, time.Now()); err != nil {
		t.Fatalf("seed ticket %s: %v", id, err)
	}
}

func gardenSeeds(t *testing.T, d *Daemon) []garden.Seed {
	t.Helper()
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("read garden: %v", err)
	}
	return read.seeds
}

func seedStrandedTicket(t *testing.T, d *Daemon, id, title, description string, status store.TicketStatus, assignee string) {
	t.Helper()
	seedBacklogTicket(t, d, id, title, description, store.TicketStatusWorking, assignee)
	if _, err := d.store.SetTicketStatus(id, status, store.TicketAuthorAttn, "", time.Now()); err != nil {
		t.Fatalf("stamp %s as %s: %v", id, status, err)
	}
}

func seedByTitle(t *testing.T, d *Daemon, title string) garden.Seed {
	t.Helper()
	for _, seed := range gardenSeeds(t, d) {
		if seed.Title == title {
			return seed
		}
	}
	t.Fatalf("no seed titled %q in %+v", title, gardenSeeds(t, d))
	return garden.Seed{}
}

func TestReplantedSeedCarriesTheReconcileVerdict(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")
	verdict := ticketReconcileCommentPrefix + " verdict — the branch is pushed, the PR is not opened"
	if _, err := d.store.AddTicketComment("wire-the-thing", store.TicketAuthorAttn, verdict, time.Now()); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}

	d.replantStrandedTickets()

	seed := seedByTitle(t, d, "Wire the thing")
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "the branch is pushed, the PR is not opened") {
		t.Fatalf("replanted seed dropped the verdict that explains it: %+v", notes)
	}
}

func TestStrandedReplantLeavesTheRestOfTheBoardAlone(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "in-flight", "In flight", "being worked", store.TicketStatusWorking, "sess-a")
	seedBacklogTicket(t, d, "finished", "Finished", "already shipped", store.TicketStatusDone, "sess-a")
	if _, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title: "Automation run", Description: "a run's own ticket",
		Status: store.TicketStatusWorking, Assignee: "sess-auto", AutomationRunID: "run-1",
	}, "automation-run", "chief", store.TicketRoleChiefOfStaff, nil, time.Now()); err != nil {
		t.Fatalf("seed automation ticket: %v", err)
	}
	if _, err := d.store.SetTicketStatus("automation-run", store.TicketStatusCrashed, store.TicketAuthorAttn, "", time.Now()); err != nil {
		t.Fatalf("stamp the automation ticket crashed: %v", err)
	}

	d.replantStrandedTickets()

	if seeds := gardenSeeds(t, d); len(seeds) != 0 {
		t.Fatalf("replant planted seeds it should not have: %+v", seeds)
	}
	for _, id := range []string{"in-flight", "finished", "automation-run"} {
		ticket, err := d.store.GetTicket(id)
		if err != nil || ticket == nil {
			t.Fatalf("GetTicket %s: %v %v", id, ticket, err)
		}
		if ticket.ArchivedAt != nil {
			t.Fatalf("replant archived %s: %+v", id, ticket)
		}
	}
}

func TestReconcilingADeathReplantsTheTicketIntoTheGarden(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"tests pass except e2e"}`),
		}, nil
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       "wire-the-thing",
		StatusAtClaim:  store.TicketStatusCrashed,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	seed := seedByTitle(t, d, "Wire the thing")
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "e2e spec never ran") {
		t.Fatalf("seed planted before the verdict landed on it: %+v", notes)
	}
	ticket, err := d.store.GetTicket("wire-the-thing")
	if err != nil || ticket == nil || ticket.ArchivedAt != nil || ticket.Status != store.TicketStatusCrashed {
		t.Fatalf("recovery rewrote the reconciled legacy ticket: %+v (%v)", ticket, err)
	}
}
