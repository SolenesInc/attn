package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARetiredMembersSleepRequestReachesItsConversationDuringBinding(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	sleep := s.PauseAt(pausepoint.MemberRetiredClearSleep)
	binding := s.PauseAt(pausepoint.ConversationCandidateStart)
	s.Start()
	app, cli := s.App(), s.Client()
	if _, err := cli.CrewCreate(protocol.CrewCreateMessage{Name: "Keel"}); err != nil {
		t.Fatal(err)
	}
	wake := wakeCrew(t, cli, "Keel", "")
	agent := s.Launched(string(wake.SessionID))
	agent.Prompted()
	if _, err := cli.CrewRetire("Keel"); err != nil {
		t.Fatal(err)
	}
	probe := uuid.NewString()
	cleared := testworld.Request(app, protocol.PtyInputMessage{
		Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(string(wake.SessionID))), Data: "/clear\r",
		Source: protocol.Ptr("agent"), ProbeID: &probe,
	}, protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
	if !cleared.Success {
		t.Fatalf("clear input = %+v", cleared)
	}
	if got := agent.Prompted(); got != "/clear" {
		t.Fatalf("clear prompt = %q", got)
	}
	sleep.Await()
	binding.Await()
	next := awaitSuccessor(app, string(wake.SessionID))
	sleep.Release()
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("retired successor received %q", got)
	}
	mail := readInbox(t, cli, string(next.ID), 0)
	if len(mail.Items) != 1 || !strings.Contains(mail.Items[0].Content, "user is asking you to close") {
		t.Fatalf("retired successor mail = %+v", mail)
	}
	binding.Release()
}
