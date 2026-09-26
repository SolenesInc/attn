package main_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var retiredTicketVerbs = []string{"status", "attach", "attach-plan", "new", "comment", "subscribe", "unsubscribe", "take"}

func commentTicket(t *testing.T, cli *client.Client, author, id, body string) {
	t.Helper()
	if _, err := cli.CommentTicket(author, id, body); err != nil {
		t.Fatalf("%s comments on %s: %v", author, id, err)
	}
}

func isBacklogSeed(seed protocol.Seed) bool {
	return seed.Title == "Backlog from before the garden"
}

func TestRetiredTicketsPointToTheGardenWhileTheirHistoryStaysReadable(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	for _, verb := range retiredTicketVerbs {
		signpost := s.Attn("ticket", verb)
		if signpost.Code != 2 {
			t.Errorf("attn ticket %s exited %d, want 2", verb, signpost.Code)
		}
		requireLines(t, "attn ticket "+verb, signpost.Stderr, "attn ticket "+verb+" retired", "attn seed ",
			"Legacy ticket reads still work: `attn ticket show <id>`, `attn ticket list`, and `attn ticket inbox`.")
	}
	signposted := slices.Sorted(slices.Values(retiredTicketVerbs))
	requireStdout(t, s.Attn("ticket", "--help"), "Tickets retired", "  list [--status", "  show <ticket-id>", "  inbox [--session",
		"Every other verb ("+strings.Join(signposted, ", ")+") is a signpost")
	for _, row := range []struct {
		args []string
		want []string
	}{
		{[]string{"delegate", "--ticket", "some-ticket", "--model", "opus"}, []string{"--ticket retired", "attn seed plant"}},
		{[]string{"delegate", "--brief", "do a thing", "--confirm", "--model", "opus"}, []string{"--confirm retired", "attn seed tend"}},
	} {
		refused := s.Attn(row.args...)
		if refused.Code != 2 || !strings.HasPrefix(refused.Stderr, "delegate: ") {
			t.Errorf("attn %q exited %d with stderr %q, want a refusal", row.args, refused.Code, refused.Stderr)
		}
		requireLines(t, strings.Join(row.args, " "), refused.Stderr, row.want...)
	}

	s.Start()
	if _, err := s.Client().CreateTicket("planner", "Backlog from before the garden", "", "backlog"); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Start()
	app := s.App()
	if !slices.ContainsFunc(app.Initial.Seeds, isBacklogSeed) {
		testworld.Await(app, protocol.EventGardenSeedsUpdated, func(m protocol.GardenSeedsUpdatedMessage) bool {
			return slices.ContainsFunc(m.Seeds, isBacklogSeed)
		})
	}
	cli := s.Client()
	if _, err := cli.CreateTicket("planner", "Price the order", "Move the prices to the new table", "pricing"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SetTicketStatus("worker", string(protocol.DispatchWorkStateReadyForReview), "ready", "pricing"); err != nil {
		t.Fatal(err)
	}
	verdict := strings.Repeat("this is a long verdict line. ", 20) + "\nsecond line\nthird line: the conclusion"
	commentTicket(t, cli, "reviewer", "pricing", verdict)

	unattended := s.Attn("ticket", "inbox", "--session", "planner")
	requireStdout(t, unattended, "pricing\n", " status_changed by worker (todo → in_review)\n", " commented by reviewer\n", verdict)
	if strings.Contains(unattended.Stdout, "user: active") {
		t.Errorf("the inbox claims user activity nobody reported:\n%s", unattended.Stdout)
	}

	app.Send(protocol.SessionSelectedMessage{Cmd: protocol.CmdSessionSelected, ID: "planner"})
	report := s.Path("report.md")
	if err := os.MkdirAll(s.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, []byte("the report"), 0o644); err != nil {
		t.Fatal(err)
	}
	shown, err := cli.ShowTicket("", "pricing")
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	completed := protocol.DispatchWorkStateCompleted
	attached := testworld.Request(app, protocol.TicketAttachMessage{
		Cmd: protocol.CmdTicketAttach, SourceSessionID: "you", TicketID: protocol.Ptr("pricing"),
		Files: []protocol.TicketAttachFile{{SourcePath: report, Filename: "report.md"}}, State: &completed,
		RequestID: protocol.Ptr(requestID), ExpectedEventSeq: shown.LatestEventSeq,
	}, protocol.EventTicketAttachResult, func(r protocol.TicketAttachResultMessage) bool { return r.RequestID == requestID })
	if !attached.Success {
		t.Fatalf("attach from the app: %s", protocol.Deref(attached.Error))
	}

	present := s.Attn("ticket", "inbox", "--session", "planner")
	if code := present.Code; code != 0 || !regexp.MustCompile(`^user: active \d+s ago\npricing\n`).MatchString(present.Stdout) {
		t.Errorf("after the user selected a session the inbox exited %d and printed:\n%s", code, present.Stdout)
	}

	requireStdout(t, s.Attn("ticket", "show", "pricing"),
		"pricing\t", "Price the order\n", "\nMove the prices to the new table\n", "activity:\n",
		" status_change by worker (", "comment by reviewer\n", verdict, "\nartifacts:\n  report.md (")

	if _, err := cli.CreateTicket("planner", "A fresh backlog item", "", "fresh"); err != nil {
		t.Fatal(err)
	}
	fresh := s.Attn("ticket", "show", "fresh")
	requireStdout(t, fresh, "fresh\t", "A fresh backlog item\n", "\nno activity\n")
	if strings.Contains(fresh.Stdout, "artifacts:") {
		t.Errorf("a ticket without artifacts prints an artifacts section:\n%s", fresh.Stdout)
	}
	board := s.Attn("ticket", "list")
	requireStdout(t, board, "pricing\t", "fresh\t")
	if strings.Contains(board.Stdout, "backlog\t") {
		t.Errorf("the board lists the ticket the cutover archived:\n%s", board.Stdout)
	}
	requireStdout(t, s.Attn("ticket", "list", "--all"), "backlog\tdone\t")
	requireStdout(t, s.Attn("ticket", "show", "backlog"), "backlog\tdone\t", " status_change by attn (todo → done)\n", "at the garden cutover; the work continues there\n")

	if _, err := cli.CreateTicket("watcher", "Watch the prices", "", "watched"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.TakeTicket("courier", "watched", false); err != nil {
		t.Fatal(err)
	}
	watch := s.Launch(testworld.Invocation{Args: []string{"ticket", "inbox", "--watch", "--interval", "20ms", "--session", "watcher"}})
	const outage = "ticket inbox --watch: connect to daemon at "
	commentTicket(t, cli, "reviewer", "watched", "first look")
	watch.AwaitStdout("first look")
	s.Stop()
	watch.AwaitStderr(outage)
	s.Start()
	commentTicket(t, s.Client(), "reviewer", "watched", "second look")
	watch.AwaitStdout("second look")
	stdout, firstOutage := watch.Output()
	if strings.Count(stdout, "first look") != 1 || strings.Count(stdout, "second look") != 1 {
		t.Errorf("the watch printed each comment other than once:\n%s", stdout)
	}
	reported := strings.Count(firstOutage, outage)
	s.Stop()
	watch.AwaitStderrCount(outage, reported+1)
	_, stderr := watch.Output()
	for _, reports := range []string{firstOutage, strings.TrimPrefix(stderr, firstOutage)} {
		lines := strings.Split(strings.TrimSuffix(reports, "\n"), "\n")
		for i := 1; i < len(lines); i++ {
			if lines[i] == lines[i-1] {
				t.Errorf("an outage repeated the same error:\n%s", reports)
			}
		}
	}
}
