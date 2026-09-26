package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func presentWait(t *testing.T, s *testworld.Stack, repo, title string) *testworld.Running {
	t.Helper()
	manifest := filepath.Join(repo, title+".present.yml")
	yaml := fmt.Sprintf("version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: HEAD\n  head: HEAD\n", title, repo)
	if err := os.WriteFile(manifest, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	waiting := s.Launch(testworld.Invocation{Args: []string{"present", "--manifest", manifest, "--wait"}, Session: "presenter"})
	waiting.AwaitStderr(fmt.Sprintf("waiting for review of round 1 of %q...", title))
	return waiting
}

func presentationRoundID(app *testworld.Peer, title string) (presentationID, roundID string) {
	app.T.Helper()
	listed := testworld.Request(app, protocol.GetPresentationsMessage{Cmd: protocol.CmdGetPresentations}, protocol.EventGetPresentationsResult,
		func(protocol.GetPresentationsResultMessage) bool { return true })
	for _, p := range listed.Presentations {
		if p.Title == title {
			round := testworld.Request(app, protocol.GetPresentationRoundMessage{Cmd: protocol.CmdGetPresentationRound, PresentationID: p.ID, Seq: protocol.Ptr(1)},
				protocol.EventGetPresentationRoundResult, func(protocol.GetPresentationRoundResultMessage) bool { return true })
			return p.ID, round.Round.ID
		}
	}
	app.T.Fatalf("no presentation titled %q in %+v", title, listed.Presentations)
	return "", ""
}

func TestPresentWaitOutlivesADaemonRestartAndPrintsTheReviewOnce(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	repo := s.Path("shop")
	gitRepo(t, repo)

	reviewed := presentWait(t, s, repo, "Checkout")
	closed := presentWait(t, s, repo, "Pricing")
	app := s.App()
	_, checkoutRound := presentationRoundID(app, "Checkout")
	pricing, _ := presentationRoundID(app, "Pricing")

	s.Stop()
	for _, waiting := range []*testworld.Running{reviewed, closed} {
		waiting.AwaitStderr("present --wait: ")
		if stdout, _ := waiting.Output(); stdout != "" {
			t.Errorf("present --wait printed %q before any review", stdout)
		}
	}
	s.Start()
	app = s.App()
	const comment = "rename the discount field before it ships"
	submitted := testworld.Request(app, protocol.PresentSubmitRoundMessage{Cmd: protocol.CmdPresentSubmitRound, RoundID: checkoutRound, Verdict: "feedback",
		Comments: []protocol.PresentCommentInput{{Filepath: "README.md", LineStart: 1, LineEnd: 1, Side: "new", Content: comment}}},
		protocol.EventPresentSubmitRoundResult, func(r protocol.PresentSubmitRoundResultMessage) bool { return r.RoundID == checkoutRound })
	if !submitted.Success {
		t.Fatalf("submitting the checkout review: %s", protocol.Deref(submitted.Error))
	}
	closing := testworld.Request(app, protocol.PresentCloseMessage{Cmd: protocol.CmdPresentClose, PresentationID: pricing},
		protocol.EventPresentCloseResult, func(r protocol.PresentCloseResultMessage) bool { return r.PresentationID == pricing })
	if !closing.Success {
		t.Fatalf("closing the pricing presentation: %s", protocol.Deref(closing.Error))
	}

	feedback := reviewed.Wait()
	if feedback.Code != 0 || strings.Count(feedback.Stdout, comment) != 1 {
		t.Errorf("present --wait on the reviewed round exited %d and printed:\n%s\nwant the review once", feedback.Code, feedback.Stdout)
	}
	withdrawn := closed.Wait()
	const closedLine = "presentation closed by the reviewer without feedback — drafts were discarded; open a new round to re-present\n"
	if withdrawn.Code != 0 || withdrawn.Stdout != closedLine {
		t.Errorf("present --wait on the closed presentation exited %d and printed %q, want %q", withdrawn.Code, withdrawn.Stdout, closedLine)
	}
}
