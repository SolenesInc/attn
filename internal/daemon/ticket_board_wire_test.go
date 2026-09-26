package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCreatingATicketMintsAnUnassignedTodoUnderItsTitleSlug(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()

		var minted []string
		for range 2 {
			created, err := cli.CreateTicket("planner", "Migrate store to X", "the brief", "")
			if err != nil {
				t.Fatal(err)
			}
			if created.Status != protocol.TicketStatusTodo {
				t.Errorf("%s was created %q, want todo", created.TicketID, created.Status)
			}
			minted = append(minted, created.TicketID)
		}
		if !slices.Equal(minted, []string{"migrate-store-to-x", "migrate-store-to-x-2"}) {
			t.Fatalf("two tickets titled alike got %q, want the slug and the slug suffixed -2", minted)
		}
		if assignee := showTicket(t, cli, "migrate-store-to-x").Assignee; assignee != "" {
			t.Errorf("a new ticket is assigned to %q, want nobody", assignee)
		}
		if _, err := cli.TakeTicket("worker", "migrate-store-to-x", false); err != nil {
			t.Fatal(err)
		}
		inbox, err := cli.TicketInbox("worker")
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox.Bundles) != 1 || len(inbox.Bundles[0].Events) != 1 ||
			inbox.Bundles[0].Events[0].Kind != protocol.TicketEventKindCreated || inbox.Bundles[0].Events[0].Author != "planner" {
			t.Errorf("the taker's inbox = %+v, want the ticket's creation by planner", inbox.Bundles)
		}

		if _, err := cli.CreateTicket("planner", "Something else", "", "migrate-store-to-x"); err == nil || !strings.Contains(err.Error(), "already taken") {
			t.Errorf("creating with a taken explicit id = %v, want a refusal saying it is already taken", err)
		}

		for range 50 {
			createTicket(t, cli, "planner", "attn", "")
		}
		var fallbacks []string
		for range 2 {
			created, err := cli.CreateTicket("planner", "attn", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if suffix, ok := strings.CutPrefix(created.TicketID, "attn-"); !ok || len(suffix) != 6 {
				t.Fatalf("past fifty sequential ids the next ticket got %q, want attn- and a six-character random suffix", created.TicketID)
			}
			fallbacks = append(fallbacks, created.TicketID)
		}
		if fallbacks[0] == fallbacks[1] {
			t.Errorf("both tickets past the sequential range got %q", fallbacks[0])
		}
	})
}

func TestTicketShowReturnsTheWholeRecordAndRefusesAnUnknownID(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		notebook := filepath.Join(w.Dir, "notebook")
		if _, err := cli.CreateTicket("planner", "Migrate the store", "Move to X", "store-migration"); err != nil {
			t.Fatal(err)
		}
		reportTicket(t, cli, "worker", "store-migration", protocol.DispatchWorkStateReadyForReview, "ready for a look")
		verdict := "line one of the verdict\nline two with more detail\nline three: the conclusion"
		commentOnTicket(t, cli, "reviewer", "store-migration", verdict)
		writeTicketNotebookFile(t, notebook, "store-migration", "report.md", "the findings")

		ticket := showTicket(t, cli, "store-migration")
		if ticket.ID != "store-migration" || ticket.Description != "Move to X" || ticket.Status != protocol.TicketStatusInReview {
			t.Errorf("show = %s %q %s, want store-migration with its brief in review", ticket.ID, ticket.Description, ticket.Status)
		}
		if got := activityLines(ticket); !slices.Equal(got, []string{"worker status_change ready for a look", "reviewer comment " + verdict}) {
			t.Errorf("activity = %q, want the report and the multi-line comment intact", got)
		}
		if len(ticket.Artifacts) != 1 || ticket.Artifacts[0].Filename != "report.md" {
			t.Errorf("artifacts = %+v, want report.md", ticket.Artifacts)
		}

		if missing, err := cli.ShowTicket("", "nope"); err == nil || !strings.Contains(err.Error(), "not found") || missing != nil {
			t.Errorf("showing an unknown ticket = %+v, %v; want a not-found refusal and no ticket", missing, err)
		}
	})
}

func TestTicketListIsTheWholeBoardWithBriefsFilteredByStatus(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		for _, id := range []string{"task-y", "task-x"} {
			if _, err := cli.CreateTicket("planner", id, "brief for "+id, id); err != nil {
				t.Fatal(err)
			}
			if _, err := cli.TakeTicket("worker-"+id, id, false); err != nil {
				t.Fatal(err)
			}
			inboxLines(t, cli, "worker-"+id)
			reportTicket(t, cli, "worker-"+id, id, protocol.DispatchWorkStateInProgress, "")
		}
		reportTicket(t, cli, "worker-task-y", "task-y", protocol.DispatchWorkStateReadyForReview, "ready")

		board, err := cli.TicketList("", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := ticketBoardRows(board); !slices.Equal(got, []string{"task-x working brief for task-x", "task-y in_review brief for task-y"}) {
			t.Errorf("the board without a session = %q, want both tickets with their briefs", got)
		}
		for status, want := range map[protocol.TicketStatus][]string{
			protocol.TicketStatusInReview: {"task-y in_review brief for task-y"},
			protocol.TicketStatusWorking:  {"task-x working brief for task-x"},
		} {
			filtered, err := cli.TicketList("", string(status), false)
			if err != nil {
				t.Fatal(err)
			}
			if got := ticketBoardRows(filtered); !slices.Equal(got, want) {
				t.Errorf("the board filtered to %s = %q, want %q", status, got, want)
			}
		}
	})
}

func TestTicketArtifactsAreTheNotebookFolderAsItIsNow(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		notebook := filepath.Join(w.Dir, "notebook")
		createTicket(t, cli, "planner", "Filesystem", "filesystem")
		for name, body := range map[string]string{
			"b.md": "b", "a.md": "a", ".hidden.md": "hidden", "notes.txt": "text", "prototype.html": "<h1>prototype</h1>", "nested/nested.md": "nested",
		} {
			writeTicketNotebookFile(t, notebook, "filesystem", name, body)
		}
		folder := filepath.Join(notebook, "tickets", "filesystem")
		if err := os.Symlink(filepath.Join(folder, "a.md"), filepath.Join(folder, "link.md")); err != nil {
			t.Fatal(err)
		}

		if got := ticketArtifactNames(showTicket(t, cli, "filesystem")); !slices.Equal(got, []string{"a.md", "b.md", "notes.txt", "prototype.html"}) {
			t.Fatalf("artifacts = %q, want the regular, visible files at the top of the folder, sorted", got)
		}
		if err := os.Rename(filepath.Join(folder, "a.md"), filepath.Join(folder, "implementation.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(folder, "b.md")); err != nil {
			t.Fatal(err)
		}
		if got := ticketArtifactNames(showTicket(t, cli, "filesystem")); !slices.Equal(got, []string{"implementation.md", "notes.txt", "prototype.html"}) {
			t.Errorf("artifacts after a rename and a delete = %q", got)
		}
	})
}

func writeTicketNotebookFile(t *testing.T, notebook, ticketID, name, body string) {
	t.Helper()
	path := filepath.Join(notebook, "tickets", ticketID, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ticketArtifactNames(ticket *protocol.Ticket) []string {
	names := make([]string, 0, len(ticket.Artifacts))
	for _, artifact := range ticket.Artifacts {
		names = append(names, artifact.Filename)
	}
	return names
}

func ticketBoardRows(tickets []protocol.Ticket) []string {
	rows := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		rows = append(rows, strings.Join([]string{ticket.ID, string(ticket.Status), ticket.Description}, " "))
	}
	slices.Sort(rows)
	return rows
}
