package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARetriedTicketCommentNotifiesOnceWhileDistinctCommentsEachNotify(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		createTicket(t, cli, "planner", "Price the order", "pricing")

		for _, body := range []string{"A", "B", "A", "A"} {
			commentOnTicket(t, cli, "reviewer", "pricing", body)
		}

		if got := inboxLines(t, cli, "planner"); !slices.Equal(got, []string{
			"pricing commented A", "pricing commented B", "pricing commented A",
		}) {
			t.Errorf("the creator was told %q, want A, B and A once each", got)
		}
	})
}

func TestTicketsAndWhatEachParticipantReadSurviveARestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		createTicket(t, cli, "planner", "Price the order", "pricing")
		commentOnTicket(t, cli, "worker", "pricing", "on it")
		reportTicket(t, cli, "worker", "pricing", protocol.DispatchWorkStateReadyForReview, "ready")
		if got := inboxLines(t, cli, "planner"); len(got) != 2 {
			t.Fatalf("before the restart the creator was told %q, want the comment and the report", got)
		}

		w.restart()
		cli = w.Client()

		if got := inboxLines(t, cli, "planner"); len(got) != 0 {
			t.Errorf("the restart redelivered %q", got)
		}
		ticket := showTicket(t, cli, "pricing")
		if ticket.Status != protocol.TicketStatusInReview {
			t.Errorf("status after the restart = %q, want in_review", ticket.Status)
		}
		if got := activityLines(ticket); !slices.Equal(got, []string{"worker comment on it", "worker status_change ready"}) {
			t.Errorf("activity after the restart = %q", got)
		}
		commentOnTicket(t, cli, "reviewer", "pricing", "one more thing")
		if got := inboxLines(t, cli, "planner"); !slices.Equal(got, []string{"pricing commented one more thing"}) {
			t.Errorf("after the restart the creator was told %q, want only the new comment", got)
		}
	})
}

func TestAnAppEditAgainstAStaleTicketIsRefusedAndChangesNothing(t *testing.T) {
	notebook := t.TempDir()
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "notebook.root", notebook)
		createTicket(t, cli, "planner", "Price the order", "pricing")
		opened := showTicket(t, cli, "pricing")
		commentOnTicket(t, cli, "worker", "pricing", "landed after you opened it")
		plan := writeAttachment(t, "plan.md", "the plan")

		stale := attachFromApp(app, "pricing", plan, protocol.Deref(opened.LatestEventSeq))
		if stale.Success || !strings.Contains(protocol.Deref(stale.Error), "changed since it was opened") {
			t.Fatalf("attach against the stale ticket = %+v, want it refused as changed", stale)
		}
		after := showTicket(t, cli, "pricing")
		if after.Status != protocol.TicketStatusTodo || len(after.Artifacts) != 0 {
			t.Fatalf("the refused attach left status %q and artifacts %+v", after.Status, after.Artifacts)
		}

		if fresh := attachFromApp(app, "pricing", plan, protocol.Deref(after.LatestEventSeq)); !fresh.Success {
			t.Fatalf("attach against the current ticket = %+v", fresh)
		}
		if done := showTicket(t, cli, "pricing"); done.Status != protocol.TicketStatusDone || len(done.Artifacts) != 1 {
			t.Errorf("the current attach left status %q and artifacts %+v", done.Status, done.Artifacts)
		}
	})
}

func TestTicketCreateRefusesAnExplicitIDThatIsNotASlug(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()

	_, err := cli.CreateTicket("planner", "Price the order", "", "Has Spaces")
	if err == nil || !strings.Contains(err.Error(), "lowercase letters, digits, and hyphens") {
		t.Fatalf("creating a ticket with id %q = %v, want a refusal that names the allowed characters", "Has Spaces", err)
	}
	if tickets, err := cli.TicketList("planner", "", true); err != nil || len(tickets) != 0 {
		t.Errorf("the board after the refusal = %+v, %v", tickets, err)
	}
}

func TestAConvertedTicketMovedBackToWorkReturnsToTheBoard(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		cli := w.Client()
		createTicket(t, cli, "planner", "Price the order", "pricing")
		w.restart()
		cli = w.Client()
		if board := boardTicketIDs(t, cli); len(board) != 0 {
			t.Fatalf("the board after the garden cutover = %v, want the converted ticket hidden", board)
		}
		if archived := ticketByID(t, cli, "pricing"); archived.ArchivedAt == nil {
			t.Fatalf("the converted ticket = %+v, want it archived", archived)
		}

		reportTicket(t, cli, "planner", "pricing", protocol.DispatchWorkStateInProgress, "back to it")

		if board := boardTicketIDs(t, cli); !slices.Equal(board, []string{"pricing"}) {
			t.Fatalf("the board after reopening = %v, want the ticket back", board)
		}
		if reopened := ticketByID(t, cli, "pricing"); reopened.ArchivedAt != nil || reopened.ClosedAt != nil || reopened.Status != protocol.TicketStatusWorking {
			t.Errorf("the reopened ticket = %+v, want it working, open and unarchived", reopened)
		}
	})
}

func TestEveryOpenTicketOfASessionThatDiesMidFlightCrashesAndSettledOnesStay(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	w.finishStartupWork()
	app, cli := w.App(), w.Client()
	worker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(worker)
	app.TypeLine(worker, "keep going")
	agent.Prompted()
	testworld.AwaitSession(app, worker, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	for _, id := range []string{"checkout", "invoices", "shipped"} {
		createTicket(t, cli, "planner", id, id)
		if _, err := cli.TakeTicket(worker, id, false); err != nil {
			t.Fatalf("take %s: %v", id, err)
		}
	}
	if got := inboxLines(t, cli, worker); !slices.Equal(got, []string{"checkout created", "invoices created", "shipped created"}) {
		t.Fatalf("before reporting, the worker was told %q, want the three tickets it took", got)
	}
	reportTicket(t, cli, worker, "shipped", protocol.DispatchWorkStateCompleted, "done")

	agent.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == worker })

	for id, want := range map[string]protocol.TicketStatus{
		"checkout": protocol.TicketStatusCrashed,
		"invoices": protocol.TicketStatusCrashed,
		"shipped":  protocol.TicketStatusDone,
	} {
		if got := showTicket(t, cli, id).Status; got != want {
			t.Errorf("%s after the agent died mid-flight = %q, want %q", id, got, want)
		}
	}
}

func TestASessionThatJoinsTheCrewKeepsItsTicketThreadsWithoutReplayingThem(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		writeCrewCharter(t, w, "trellis")
		w.restart()
		cli := w.Client()
		for _, day := range []string{"day-a", "day-b"} {
			if err := cli.Register(day, day, w.Path(day)); err != nil {
				t.Fatal(err)
			}
		}
		createTicket(t, cli, "day-a", "Own thread", "own-thread")
		commentOnTicket(t, cli, "day-a", "own-thread", "my own note")
		createTicket(t, cli, "planner", "Watched", "watched")
		for _, day := range []string{"day-a", "day-b"} {
			if _, err := cli.SubscribeTicket(day, "watched"); err != nil {
				t.Fatal(err)
			}
		}
		commentOnTicket(t, cli, "planner", "watched", "already read")
		for _, day := range []string{"day-a", "day-b"} {
			if got := inboxLines(t, cli, day); !slices.Contains(got, "watched commented already read") {
				t.Fatalf("before joining, %s was told %q", day, got)
			}
		}
		commentOnTicket(t, cli, "planner", "own-thread", "while you were away")

		if err := cli.RegisterAsMember("day-a", "day-a", w.Path("day-a"), "", "trellis"); err != nil {
			t.Fatalf("join the crew as trellis: %v", err)
		}

		if got := inboxLines(t, cli, "day-a"); !slices.Equal(got, []string{"own-thread commented while you were away"}) {
			t.Errorf("after joining, the member was told %q, want only the unread comment from someone else", got)
		}
		if got := activityLines(showTicket(t, cli, "own-thread")); !slices.Equal(got, []string{
			"member:trellis comment my own note", "planner comment while you were away",
		}) {
			t.Errorf("own-thread activity = %q, want the earlier note attributed to the member", got)
		}
		commentOnTicket(t, cli, "planner", "watched", "still following?")
		if got := inboxLines(t, cli, "day-a"); !slices.Equal(got, []string{"watched commented still following?"}) {
			t.Errorf("the member was told %q, want new activity on the ticket it followed", got)
		}

		if err := cli.Unregister("day-a"); err != nil {
			t.Fatal(err)
		}
		if err := cli.RegisterAsMember("day-b", "day-b", w.Path("day-b"), "", "trellis"); err != nil {
			t.Fatalf("day-b wakes as trellis: %v", err)
		}
		if got := inboxLines(t, cli, "day-b"); len(got) != 0 {
			t.Errorf("waking in a session that had read less replayed %q", got)
		}
		commentOnTicket(t, cli, "planner", "watched", "new day")
		if got := inboxLines(t, cli, "day-b"); !slices.Equal(got, []string{"watched commented new day"}) {
			t.Errorf("the member's new session was told %q, want the new comment", got)
		}
	})
}

func (w *world) finishStartupWork() {
	w.T.Helper()
	if w.bubbled {
		w.advance(0)
		return
	}
	watcher := w.App()
	for !everyTaskSettled(watcher) {
		testworld.Await[protocol.TasksChangedMessage](watcher, protocol.EventTasksChanged, nil)
	}
}

func everyTaskSettled(p *testworld.Peer) bool {
	p.T.Helper()
	requestID := uuid.NewString()
	listed := testworld.Request(p, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })
	for _, task := range listed.Tasks {
		if task.State != "done" && task.State != "dead" {
			return false
		}
	}
	return true
}

func createTicket(t *testing.T, cli *client.Client, author, title, id string) {
	t.Helper()
	if _, err := cli.CreateTicket(author, title, "", id); err != nil {
		t.Fatalf("%s creates %s: %v", author, id, err)
	}
}

func commentOnTicket(t *testing.T, cli *client.Client, author, id, body string) {
	t.Helper()
	result, err := cli.CommentTicket(author, id, body)
	if err != nil {
		t.Fatalf("%s comments %q on %s: %v", author, body, id, err)
	}
	if !result.Applied {
		t.Fatalf("%s commenting %q on %s was held for catch-up", author, body, id)
	}
}

func reportTicket(t *testing.T, cli *client.Client, author, id string, state protocol.DispatchWorkState, comment string) {
	t.Helper()
	result, err := cli.SetTicketStatus(author, string(state), comment, id)
	if err != nil {
		t.Fatalf("%s reports %s on %s: %v", author, state, id, err)
	}
	if !result.Applied {
		t.Fatalf("%s reporting %s on %s was held for catch-up", author, state, id)
	}
}

func showTicket(t *testing.T, cli *client.Client, id string) *protocol.Ticket {
	t.Helper()
	ticket, err := cli.ShowTicket("", id)
	if err != nil {
		t.Fatalf("show %s: %v", id, err)
	}
	return ticket
}

func ticketByID(t *testing.T, cli *client.Client, id string) protocol.Ticket {
	t.Helper()
	tickets, err := cli.TicketList("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, ticket := range tickets {
		if ticket.ID == id {
			return ticket
		}
	}
	t.Fatalf("%s is not listed even with archived tickets", id)
	return protocol.Ticket{}
}

func boardTicketIDs(t *testing.T, cli *client.Client) []string {
	t.Helper()
	tickets, err := cli.TicketList("", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		ids = append(ids, ticket.ID)
	}
	return ids
}

func inboxLines(t *testing.T, cli *client.Client, session string) []string {
	t.Helper()
	inbox, err := cli.TicketInbox(session)
	if err != nil {
		t.Fatalf("%s reads its ticket inbox: %v", session, err)
	}
	var lines []string
	for _, bundle := range inbox.Bundles {
		for _, event := range bundle.Events {
			lines = append(lines, strings.TrimSpace(strings.Join([]string{bundle.TicketID, string(event.Kind), protocol.Deref(event.Comment)}, " ")))
		}
	}
	return lines
}

func activityLines(ticket *protocol.Ticket) []string {
	lines := make([]string, 0, len(ticket.Activity))
	for _, a := range ticket.Activity {
		lines = append(lines, strings.TrimSpace(strings.Join([]string{a.Author, string(a.Kind), protocol.Deref(a.Comment)}, " ")))
	}
	return lines
}

func setSetting(t *testing.T, app *testworld.Peer, key, value string) {
	t.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(result.Success) {
		t.Fatalf("set %s = %s: %s", key, value, protocol.Deref(result.Error))
	}
}

func writeAttachment(t *testing.T, name, body string) protocol.TicketAttachFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return protocol.TicketAttachFile{SourcePath: path, Filename: name}
}

func attachFromApp(app *testworld.Peer, ticketID string, file protocol.TicketAttachFile, expectedSeq int) protocol.TicketAttachResultMessage {
	requestID := uuid.NewString()
	completed := protocol.DispatchWorkStateCompleted
	return testworld.Request(app, protocol.TicketAttachMessage{
		Cmd: protocol.CmdTicketAttach, SourceSessionID: "you", TicketID: protocol.Ptr(ticketID),
		Files: []protocol.TicketAttachFile{file}, State: &completed,
		RequestID: protocol.Ptr(requestID), ExpectedEventSeq: protocol.Ptr(expectedSeq),
	}, protocol.EventTicketAttachResult, func(r protocol.TicketAttachResultMessage) bool { return r.RequestID == requestID })
}

func writeCrewCharter(t *testing.T, w *world, member string) {
	t.Helper()
	home := filepath.Join(w.Dir, crew.HomesDirName, member)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, crew.CharterFileName), []byte("# "+member+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
