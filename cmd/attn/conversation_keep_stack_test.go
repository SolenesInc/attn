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

func TestConversationCLIListPinUnkeepAndForget(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	delegated, err := s.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "conversation-cli", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("A small kept conversation")},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := s.Launched(delegated.SessionID)
	first.Prompted()
	app := s.App()

	if result := s.Attn("conversation", "keep", delegated.SessionID); result.Code != 0 {
		t.Fatalf("live pin: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	pending := s.Attn("conversation", "list")
	if pending.Code != 0 || !strings.Contains(pending.Stdout, "0 kept · 0 B · 1 pending") || !strings.Contains(pending.Stdout, "copy once quiet") || !strings.Contains(pending.Stdout, first.ConversationID) {
		t.Fatalf("pending list: %+v", pending)
	}
	if result := s.Attn("conversation", "unkeep", delegated.SessionID); result.Code != 0 {
		t.Fatalf("live unpin: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if result := s.Attn("agent", "close", delegated.SessionID, "-m", "done", "--source-session", delegated.SessionID); result.Code != 0 {
		t.Fatalf("close: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	shown := s.Attn("seed", "show", delegated.SeedID)
	if shown.Code != 0 || strings.Contains(shown.Stdout, "0.0 MB") || !strings.Contains(shown.Stdout, "kept by attn (") || !(strings.Contains(shown.Stdout, " B)") || strings.Contains(shown.Stdout, " KB)")) {
		t.Fatalf("small archive display: %+v", shown)
	}
	for _, args := range [][]string{{"conversation", "keep", first.ConversationID}, {"conversation", "unkeep", delegated.SessionID}, {"conversation", "keep", delegated.SessionID}} {
		if result := s.Attn(args...); result.Code != 0 {
			t.Fatalf("%v: %+v", args, result)
		}
		testworld.AwaitTaskDone(app, "conversation_keep")
	}
	list := s.Attn("conversation", "list")
	for _, want := range []string{"1 kept · ", "CONVERSATION", first.ConversationID, "claude", "pinned ", "open seed ", delegated.SeedID, delegated.SessionID} {
		if list.Code != 0 || !strings.Contains(list.Stdout, want) {
			t.Errorf("list missing %q: %+v", want, list)
		}
	}
	if strings.Contains(list.Stdout, "0.0 MB") {
		t.Fatalf("small list size rounded to zero: %+v", list)
	}
	refused := s.Attn("conversation", "forget", delegated.SessionID)
	if refused.Code == 0 || !strings.Contains(refused.Stderr, delegated.SeedID) {
		t.Fatalf("open seed forget: %+v", refused)
	}
	if result := s.Attn("seed", "wither", delegated.SeedID, "-m", "finished"); result.Code != 0 {
		t.Fatalf("wither: %+v", result)
	}
	testworld.AwaitTaskDone(app, "conversation_keep")
	if result := s.Attn("conversation", "forget", first.ConversationID); result.Code != 0 || !strings.Contains(result.Stdout, "Claude's own files are untouched") {
		t.Fatalf("forget: %+v", result)
	}
	list = s.Attn("conversation", "list")
	if list.Code != 0 || !strings.Contains(list.Stdout, "0 kept · 0 B") || strings.Contains(list.Stdout, first.ConversationID) {
		t.Fatalf("live list: %+v", list)
	}
	all := s.Attn("conversation", "list", "--all")
	if all.Code != 0 || !strings.Contains(all.Stdout, first.ConversationID) || !strings.Contains(all.Stdout, "you deleted ") {
		t.Fatalf("tombstone list: %+v", all)
	}
	shown = s.Attn("seed", "show", delegated.SeedID)
	if shown.Code != 0 || !strings.Contains(shown.Stdout, "you deleted attn's copy") {
		t.Fatalf("seed user tombstone: %+v", shown)
	}
}
