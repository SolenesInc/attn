package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type argvRefusal struct {
	args    []string
	session string
	want    string
}

func requireRefusals(t *testing.T, s *testworld.Stack, rows []argvRefusal) {
	t.Helper()
	for _, row := range rows {
		prefix := strings.Join(row.args[:2], " ") + ": "
		refused := s.Run(testworld.Invocation{Args: row.args, Session: row.session})
		if refused.Code != 2 || !strings.HasPrefix(refused.Stderr, prefix) || !strings.Contains(refused.Stderr, row.want) {
			t.Errorf("attn %q exited %d with stderr %q, want a refusal naming %q", row.args, refused.Code, refused.Stderr, row.want)
		}
	}
}

func register(t *testing.T, s *testworld.Stack, id, label string) {
	t.Helper()
	if err := s.Client().Register(id, label, s.Path(label)); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func requireFailure(t *testing.T, got testworld.Result, prefix string, want ...string) {
	t.Helper()
	if got.Code != 1 || got.Stdout != "" || !strings.HasPrefix(got.Stderr, prefix) {
		t.Errorf("exited %d with stdout %q and stderr %q, want exit 1, no stdout and %q", got.Code, got.Stdout, got.Stderr, prefix)
	}
	requireLines(t, prefix, got.Stderr, want...)
}

func requireScreen(t *testing.T, peek, heading, want string) {
	t.Helper()
	_, screen, found := strings.Cut(peek, "\n"+heading+" (")
	if !found || !strings.Contains(screen, want) || strings.Contains(peek, "screen unavailable") {
		t.Errorf("peek does not show %q under %q:\n%s", want, heading, peek)
	}
}

func TestAgentPeekShowsASessionWithoutInterruptingIt(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"agent", "peek"}, want: "exactly one target"},
		{args: []string{"agent", "peek", "--json", "abc"}, want: "exactly one target"},
		{args: []string{"agent", "peek", "abc", "def"}, want: "exactly one target"},
		{args: []string{"agent", "peek", "abc", "--nope"}, want: "not defined: -nope"},
	})
	requireFailure(t, s.Attn("agent", "peek", "abc"), "agent peek: ", "connect to daemon at")

	s.Start()
	app := s.App()
	builder := s.Spawn(app, fakeagent.Claude, s.Path("shop"), labelled("builder"))
	claude := s.Launched(builder)
	app.TypeLine(builder, "add a discount field")
	claude.Prompted()
	if err := s.Client().UpdateTodos(builder, []string{"[✓] read the plan", "[→] build peek"}); err != nil {
		t.Fatal(err)
	}
	claude.Reply("working on it\nsecond line <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, builder, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
	})

	peek := s.Attn("agent", "peek", builder[:8])
	requireLines(t, "peek", peek.Stdout,
		"session "+builder+" (claude) — builder\n",
		"workspace: shop\n",
		"state: waiting_input (",
		"turn: owed to this session\n",
		"todos:\n  [✓] read the plan\n  [→] build peek\n",
		"last assistant message:\n  working on it\n  second line",
	)
	requireScreen(t, peek.Stdout, "screen", "working on it")
	var asJSON struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}
	s.Attn("agent", "peek", builder, "--json").JSON(t, &asJSON)
	if asJSON.SessionID != builder || asJSON.State != "waiting_input" {
		t.Errorf("peek --json = %+v", asJSON)
	}

	register(t, s, "quiet-1111-2222", "quiet")
	quiet := s.Attn("agent", "peek", "quiet-1111-2222").Stdout
	requireLines(t, "peek of a session with nothing to show", quiet, "session quiet-1111-2222 (", "state: ", "screen unavailable")
	for _, empty := range []string{"todos:", "last assistant message:", "turn:"} {
		if strings.Contains(quiet, empty) {
			t.Errorf("peek shows the empty section %q:\n%s", empty, quiet)
		}
	}

	register(t, s, "twin-aaaa", "twin a")
	register(t, s, "twin-bbbb", "twin b")
	requireFailure(t, s.Attn("agent", "peek", "twin"), "agent peek: ", `"twin" matches more than one session`)
	requireFailure(t, s.Attn("agent", "peek", "zzzz"), "agent peek: ", `"zzzz"`, "attn agent list", "attn crew list")

	claude.Exit(1)
	testworld.AwaitSession(app, builder, func(x protocol.Session) bool { return protocol.Deref(x.StateReason) == "process_exited" })
	exited := s.Attn("agent", "peek", builder).Stdout
	requireLines(t, "peek after exit", exited, "\nprocess exited with code 1 at ")
	requireScreen(t, exited, "screen at exit", "working on it")
}

func TestAgentMessagesCarryTheSenderAndTheDaemonsVerdict(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	const (
		reviewer  = "rev-1111-2222"
		desk      = "desk-3333-4444"
		bystander = "bys-5555-6666"
	)
	atLimit := strings.Repeat("x", protocol.AgentMessageMaxChars)
	requireRefusals(t, s, []argvRefusal{
		{args: []string{"agent", "msg", "desk", "hello"}, want: "--source-session"},
		{args: []string{"agent", "msg", "desk", "the", "rebase", "is", "done"}, session: reviewer, want: "quote it"},
		{args: []string{"agent", "msg", "desk"}, session: reviewer, want: "usage:"},
		{args: []string{"agent", "msg", "desk", "   "}, session: reviewer, want: "empty"},
		{args: []string{"agent", "msg", "desk", "-hello"}, session: reviewer, want: "attn agent msg -- <id>"},
		{args: []string{"agent", "msg", "desk", atLimit + "x"}, session: reviewer, want: "is 32769 bytes and the limit is 32768"},
		{args: []string{"agent", "msg-status"}, session: reviewer, want: "usage:"},
		{args: []string{"agent", "msg-status", "--json", "message-id"}, session: reviewer, want: "usage:"},
		{args: []string{"agent", "msg-status", "message-id"}, want: "--session"},
		{args: []string{"agent", "msg-status", "message-id", "other"}, session: reviewer, want: "usage:"},
		{args: []string{"agent", "inbox"}, want: "--session"},
		{args: []string{"agent", "inbox", "--limit", "0"}, session: desk, want: "between 1 and 50"},
		{args: []string{"agent", "inbox", "--limit", "51"}, session: desk, want: "between 1 and 50"},
		{args: []string{"agent", "inbox", "message-id", "--limit", "1"}, session: desk, want: "cannot be used"},
		{args: []string{"agent", "inbox", ""}, session: desk, want: "usage:"},
		{args: []string{"agent", "inbox", "one", "two"}, session: desk, want: "usage:"},
		{args: []string{"agent", "close", "desk"}, session: reviewer, want: "a close needs a reason"},
		{args: []string{"agent", "close", "desk", "-m", "   "}, session: reviewer, want: "a close needs a reason"},
		{args: []string{"agent", "close", "desk", "-m", "the", "PR", "merged"}, session: reviewer, want: "quote the reason"},
		{args: []string{"agent", "close", "-m", "done"}, session: reviewer, want: "usage:"},
		{args: []string{"agent", "close", "desk", "-m", "done"}, want: "--source-session"},
	})

	s.Start()
	app := s.App()
	register(t, s, reviewer, "reviewer")
	register(t, s, desk, "desk")
	register(t, s, bystander, "bystander")
	recipient := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(recipient)
	converse(app, claude, recipient, "wait for the reviewer", "Waiting.")

	sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient[:8], "the discount is applied after tax"}, Session: reviewer})
	notified, messageID, _ := strings.Cut(strings.TrimSpace(sent.Stdout), " (id ")
	messageID = strings.TrimSuffix(messageID, ")")
	if sent.Code != 0 || !strings.HasPrefix(notified, "notified: ") || messageID == "" {
		t.Fatalf("a message to an idle agent exited %d and printed %q", sent.Code, sent.Stdout)
	}
	claude.Prompted()
	if read := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", messageID}, Session: recipient}); read.Code != 0 {
		t.Errorf("the recipient reading its message exited %d: %s", read.Code, read.Stderr)
	} else {
		requireLines(t, "the recipient's read", read.Stdout, "rev-1111 (reviewer)", "the discount is applied after tax")
	}
	var reread protocol.AgentPeerMessage
	s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json", messageID}, Session: recipient}).JSON(t, &reread)
	if reread.MessageID != messageID || reread.SenderSessionID != reviewer || reread.Content != "the discount is applied after tax" {
		t.Errorf("a read by id with --json first = %+v", reread)
	}
	duplicate := s.Run(testworld.Invocation{Args: []string{"agent", "msg", recipient, "the discount is applied after tax"}, Session: reviewer})
	if duplicate.Code != 1 || !strings.HasPrefix(duplicate.Stdout, "refused: ") || !strings.Contains(duplicate.Stdout, "already sent that exact text") || strings.Contains(duplicate.Stdout, "(id ") {
		t.Errorf("a duplicate exited %d and printed %q, want a refusal with its reason and no id", duplicate.Code, duplicate.Stdout)
	}

	var queued protocol.AgentMsgResult
	dashed := s.Run(testworld.Invocation{Args: []string{"agent", "msg", "--", desk, "-hello", "--json"}, Session: reviewer})
	dashed.JSON(t, &queued)
	if dashed.Code != 0 || queued.Status != protocol.AgentMsgStatusQueued || queued.MessageID == "" {
		t.Fatalf("a dash-leading message to a session with no terminal exited %d with %+v, want it queued", dashed.Code, queued)
	}
	if long := s.Run(testworld.Invocation{Args: []string{"agent", "msg", desk, atLimit, "--source-session", bystander}, Session: reviewer}); long.Code != 0 {
		t.Errorf("a message exactly at the limit exited %d: %s", long.Code, long.Stderr)
	}

	if status := s.Run(testworld.Invocation{Args: []string{"agent", "msg-status", queued.MessageID}, Session: reviewer}); status.Stdout != "queued: message "+queued.MessageID+" to session desk-333\n" {
		t.Errorf("msg-status exited %d and printed %q", status.Code, status.Stdout)
	}
	var status protocol.AgentPeerMessage
	s.Attn("agent", "msg-status", queued.MessageID, "--session", reviewer, "--json").JSON(t, &status)
	if status.MessageID != queued.MessageID || status.SenderSessionID != reviewer || status.TargetSessionID != desk || status.State != protocol.AgentMessageStateQueued {
		t.Errorf("msg-status --session %s --json = %+v", reviewer, status)
	}
	requireFailure(t, s.Run(testworld.Invocation{Args: []string{"agent", "msg-status", queued.MessageID}, Session: bystander}),
		"agent msg-status: ", "not found for this session")
	requireFailure(t, s.Run(testworld.Invocation{Args: []string{"agent", "inbox", queued.MessageID}, Session: desk}),
		"agent inbox: ", "is still queued")

	var batch protocol.AgentInboxBatchResult
	s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--session", desk, "--limit", "1", "--json"}, Session: bystander}).JSON(t, &batch)
	if len(batch.Items) != 1 || batch.Items[0].Content != "-hello" || protocol.Deref(batch.Items[0].SenderSessionID) != reviewer || batch.Remaining != 1 {
		t.Fatalf("the first of two unread = %+v", batch)
	}
	s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: desk}).JSON(t, &batch)
	if len(batch.Items) != 1 || protocol.Deref(batch.Items[0].SenderSessionID) != bystander || len(batch.Items[0].Content) != protocol.AgentMessageMaxChars {
		t.Fatalf("the message sent with --source-session = %+v, want it attributed to %s", batch.Items, bystander)
	}

	requireFailure(t, s.Run(testworld.Invocation{Args: []string{"agent", "msg", "nobody", "hi"}, Session: reviewer}),
		"agent msg: ", `"nobody"`, "attn agent list", "attn crew list")
	requireFailure(t, s.Run(testworld.Invocation{Args: []string{"agent", "msg", desk, "hi", "--source-session", "ghost"}, Session: reviewer}),
		"agent msg: ", `the sender "ghost"`)

	var seed protocol.Seed
	s.Run(testworld.Invocation{Args: []string{"seed", "plant", "sweep the stale branches", "--json"}, Session: desk}).JSON(t, &seed)
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Session: desk}); tend.Code != 0 {
		t.Fatalf("tend exited %d: %s", tend.Code, tend.Stderr)
	}
	closed := s.Run(testworld.Invocation{Args: []string{"agent", "close", seed.ID, "-m", "its report landed"}, Session: desk})
	if closed.Code != 0 {
		t.Fatalf("close exited %d: %s", closed.Code, closed.Stderr)
	}
	requireLines(t, "close", closed.Stdout,
		"closed session desk-333 (desk): its report landed\n",
		"noted on "+seed.ID+", which it was tending\n",
		"`attn session show desk-333`",
	)

	var selfClosed protocol.AgentCloseResult
	s.Run(testworld.Invocation{Args: []string{"agent", "close", bystander, "-m", "done", "--source-session", bystander, "--json"}, Session: reviewer}).JSON(t, &selfClosed)
	if selfClosed.TargetSessionID != bystander || selfClosed.Reason != "done" {
		t.Errorf("a close whose --source-session overrides the environment = %+v", selfClosed)
	}
}
