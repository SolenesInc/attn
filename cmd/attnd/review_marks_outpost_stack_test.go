package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/review"
	"github.com/victorarias/attn/internal/testworld"
)

func gitOutput(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Both stacks share one disk, so the review is opened through /proc/self/cwd:
// each daemon resolves that path inside its own working directory, which
// gives one path with a different repository on each machine.
func TestAnOutpostAgentsReviewTakesItsAnswerAndRecordsItsSendOnTheOutpost(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gives each daemon its own repository at one path through /proc/self/cwd, which only Linux has")
	}
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	const document = "/proc/self/cwd/review.md"
	text := "---\nattn: review\nrepository: /proc/self/cwd/repo\nbase: main\nhead: main\n---\n# Calc\n\nAdd keeps its sum.\n"
	for stack, now := range map[*testworld.Stack]string{home: "package calc\n\n// home\n", outpost: "package calc\n\n// outpost sum\n"} {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
		stack.DaemonDir = stack.Path("daemon-dir")
		reviewedRepository(t, filepath.Join(stack.DaemonDir, "repo"), "package calc\n", now)
		if err := os.WriteFile(filepath.Join(stack.DaemonDir, "review.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	copied := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "code_review_enabled", "true"), "true")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "code_review_enabled"
	})
	app := pair.App
	shell := home.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })

	app.TypeLine(shell, "attn open "+document+" && printf '__opened_%s\\n' yes")
	app.AwaitScreen(shell, "__opened_yes")
	awaitOutpostTileContent(app, pair.Host, text)

	outpostURI, homeURI := "attn://file/%2Fproc%2Fself%2Fcwd%2Freview.md?host="+pair.Host, "attn://file/%2Fproc%2Fself%2Fcwd%2Freview.md"
	draftOf := func(host *string, uri string) []protocol.MarkdownAnnotation {
		got := testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{Cmd: protocol.CmdMarkdownAnnotationsGet, DocumentUri: uri, SourceKind: "file",
			Path: protocol.Ptr(document), Host: host, RequestID: uuid.NewString()},
			protocol.EventMarkdownAnnotationsGetResult, func(r protocol.MarkdownAnnotationsGetResultMessage) bool { return r.DocumentUri == uri })
		if !got.Success {
			t.Fatalf("reading the draft of %s = %+v", uri, got)
		}
		return got.Annotations
	}
	submit := func(send protocol.MarkdownAnnotationsSubmitMessage) protocol.MarkdownAnnotationsSubmitResultMessage {
		send.Cmd, send.DocumentUri, send.SourceKind, send.Path, send.Host = protocol.CmdMarkdownAnnotationsSubmit, outpostURI, "file", protocol.Ptr(document), protocol.Ptr(pair.Host)
		send.TargetSessionID, send.RequestID = protocol.Ptr(string(shell)), uuid.NewString()
		return testworld.Request(app, send, protocol.EventMarkdownAnnotationsSubmitResult,
			func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.RequestID == send.RequestID })
	}

	question := protocol.MarkdownAnnotation{ID: "q1", Type: protocol.MarkdownAnnotationQuestion, Text: protocol.Ptr("Why keep it?"), CreatedAt: 1,
		Anchor: &protocol.MarkdownAnnotationAnchor{BlockID: "b1", StartLine: 9, EndLine: 9, Exact: "Add keeps its sum."}}
	saved := testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{Cmd: protocol.CmdMarkdownAnnotationsSave, DocumentUri: outpostURI, SourceKind: "file",
		Path: protocol.Ptr(document), Host: protocol.Ptr(pair.Host), Upserts: []protocol.MarkdownAnnotation{question}, RemovedIds: []string{}, RequestID: uuid.NewString()},
		protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.DocumentUri == outpostURI })
	if !saved.Success {
		t.Fatalf("saving the question = %+v", saved)
	}

	app.TypeLine(shell, "cat > asked.txt")
	if asked := submit(protocol.MarkdownAnnotationsSubmitMessage{OnlyIds: []string{"q1"}}); asked.Status != "delivered" {
		t.Fatalf("asking on the outpost's review = %+v, want delivered", asked)
	}
	command := "attn review answer --doc " + document + " q1"
	app.AwaitScreen(shell, command)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(shell)), Data: "\x04"})

	answered := app.EventCount()
	app.TypeLine(shell, command+" -m 'It reads as a value.' && printf '__answered_%s\\n' yes")
	app.AwaitScreen(shell, "__answered_yes")
	pushed := testworld.AwaitSince(app, answered, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return protocol.Deref(m.Host) == pair.Host && m.Path == document && m.AnnotationsGeneration != nil
	})
	if marks := draftOf(protocol.Ptr(pair.Host), outpostURI); len(marks) != 1 || protocol.Deref(marks[0].Answer) != "It reads as a value." {
		t.Fatalf("the outpost's draft after the answer = %+v, want the question with its answer", marks)
	}
	if marks := draftOf(nil, homeURI); len(marks) != 0 {
		t.Fatalf("the home's draft of the same path = %+v, want it untouched", marks)
	}

	app.TypeLine(shell, "cat > sent.txt")
	if sent := submit(protocol.MarkdownAnnotationsSubmitMessage{Verdict: protocol.Ptr(protocol.ReviewVerdictApprove)}); sent.Status != "delivered" || sent.Error != nil || len(sent.Annotations) != 0 {
		t.Fatalf("sending the outpost's review = %+v, want it delivered, spent and recorded", sent)
	}
	app.AwaitScreen(shell, "You answered: It reads as a value.")

	outpostHead, err := gitOutput(t, filepath.Join(outpost.DaemonDir, "repo"), "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := gitOutput(t, filepath.Join(outpost.DaemonDir, "repo"), "rev-parse", "--verify", review.SentRef(document)); err != nil || got != outpostHead {
		t.Fatalf("the outpost's sent ref = %q (%v), want the outpost's head %s", got, err, outpostHead)
	}
	if got, err := gitOutput(t, filepath.Join(home.DaemonDir, "repo"), "rev-parse", "--verify", review.SentRef(document)); err == nil {
		t.Fatalf("the home's repository at the same path has a sent ref %s, want none", got)
	}
	t.Logf("on %s: the question went to the outpost shell, its answer reached the outpost draft (tile generation %d) and not the home's, and the sent ref names the outpost's head %s", pair.Host, protocol.Deref(pushed.AnnotationsGeneration), outpostHead)
}
