package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestKeptConversationSurvivesDaemonRestartAndResumeRestoresIt(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	delegated, err := s.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "keep-conversation", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Keep this conversation until the work is done.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := s.Launched(delegated.SessionID)
	first.Prompted()
	shared := plant(t, s, "Keep another piece of this conversation")
	if tended := s.Run(testworld.Invocation{Args: []string{"seed", "tend", shared.ID}, Session: delegated.SessionID}); tended.Code != 0 {
		t.Fatalf("tend shared conversation: %+v", tended)
	}
	first.Reply("remember this <!-- attn:state=waiting_input -->")
	paths, err := filepath.Glob(filepath.Join(s.Dir, "toolhome", ".claude", "projects", "*", first.ConversationID+".jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("transcript paths: %v, %v", paths, err)
	}
	path := paths[0]
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := s.Attn("agent", "close", delegated.SessionID, "-m", "verify durable conversation", "--source-session", delegated.SessionID)
	if closed.Code != 0 {
		t.Fatalf("close: %+v", closed)
	}
	testworld.AwaitTaskDone(s.App(), "conversation_keep")
	shown := s.Attn("seed", "show", delegated.SeedID).Stdout
	if !strings.Contains(shown, "conversation  kept by attn") || !strings.Contains(shown, "while an open seed points at it") {
		t.Fatalf("seed show:\n%s", shown)
	}
	if closedSeed := s.Attn("seed", "wither", shared.ID, "-m", "This piece is done"); closedSeed.Code != 0 {
		t.Fatalf("wither shared seed: %+v", closedSeed)
	}
	testworld.AwaitTaskDone(s.App(), "conversation_keep")
	closedShow := s.Attn("seed", "show", shared.ID).Stdout
	if !strings.Contains(closedShow, "while an open seed points at it") || strings.Contains(closedShow, "while this seed is open") {
		t.Fatalf("closed seed must describe the other open reference:\n%s", closedShow)
	}
	s.Stop()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.Start()
	result := testworld.Request(s.App(), protocol.SeedResumeMessage{
		Cmd: protocol.CmdSeedResume, SeedID: delegated.SeedID, RequestID: protocol.Ptr("resume-kept"),
	}, protocol.EventSeedResumeResult, func(result protocol.SeedResumeResultMessage) bool { return result.RequestID == "resume-kept" })
	if !result.Success {
		t.Fatalf("resume: %+v", result)
	}

	resumed := s.Launched(delegated.SessionID)
	if !resumed.Resumed || resumed.ConversationID != first.ConversationID {
		t.Fatalf("resumed conversation: %+v", resumed)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("restored transcript = %q, %v; want %q", got, err, original)
	}
}
