package daemon_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPresentationMovesThroughItsReviewRounds(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		repo := newRepo(t, "shop")

		checkout := openPresentation(t, cli, repo, "Checkout", "")
		if checkout.Seq != 1 {
			t.Fatalf("the first round of a new presentation = %+v, want seq 1", checkout)
		}
		if got := presentation(t, app, checkout.PresentationID); got.Status != "open" || got.LatestRoundSeq != 1 || got.LatestRoundSubmitted {
			t.Fatalf("a new presentation = %+v, want open with round 1 awaiting review", got)
		}
		w.advance(time.Second)
		pricing := openPresentation(t, cli, repo, "Pricing", "")
		if got := presentations(t, app); len(got) != 2 || got[0].ID != pricing.PresentationID || got[1].ID != checkout.PresentationID {
			t.Fatalf("presentations = %+v, want the newer one first", got)
		}

		second := openPresentation(t, cli, repo, "Checkout", checkout.PresentationID)
		if second.PresentationID != checkout.PresentationID || second.Seq != 2 {
			t.Fatalf("a new round on %s = %+v, want seq 2 of the same presentation", checkout.PresentationID, second)
		}
		for _, latest := range []int{0, -1} {
			if got := presentationRound(app, checkout.PresentationID, latest); !got.Success || got.Round.ID != second.RoundID {
				t.Errorf("round seq %d = %+v, want the latest round", latest, got)
			}
		}
		if got := presentationRound(app, checkout.PresentationID, 1); !got.Success || got.Round.ID != checkout.RoundID {
			t.Errorf("round seq 1 = %+v, want the first round", got)
		}
		if got := presentationRound(app, checkout.PresentationID, 99); got.Success {
			t.Errorf("round seq 99 = %+v, want it refused", got)
		}

		if bogus := submitRound(app, second.RoundID, "bogus"); bogus.Success {
			t.Errorf("a submit with verdict bogus = %+v, want it refused", bogus)
		}
		if got := presentationRound(app, checkout.PresentationID, 2); got.Round.SubmittedAt != nil {
			t.Errorf("round 2 after the refused verdict = %+v, want it still awaiting review", got.Round)
		}
		feedback := submitRound(app, second.RoundID, "feedback",
			protocol.PresentCommentInput{Filepath: "b.go", LineStart: 10, LineEnd: 10, Side: "new", Content: "b comment"},
			protocol.PresentCommentInput{Filepath: "a.go", LineStart: 20, LineEnd: 20, Side: "old", Content: "a later line"},
			protocol.PresentCommentInput{Filepath: "a.go", LineStart: 5, LineEnd: 5, Side: "new", Content: "a earlier line"},
		)
		if !feedback.Success {
			t.Fatalf("submitting feedback: %s", protocol.Deref(feedback.Error))
		}
		reviewed := presentationRound(app, checkout.PresentationID, 2)
		if reviewed.Round.SubmittedAt == nil || protocol.Deref(reviewed.Round.Verdict) != "feedback" {
			t.Errorf("round 2 after feedback = %+v, want it submitted with the feedback verdict", reviewed.Round)
		}
		if got := commentLocations(reviewed.Comments); fmt.Sprint(got) != "[a.go:5 by user a.go:20 by user b.go:10 by user]" {
			t.Errorf("round 2 comments = %v, want the user's three comments in file and line order", got)
		}
		if got := presentation(t, app, checkout.PresentationID); got.Status != "open" || got.LatestRoundSeq != 2 || !got.LatestRoundSubmitted {
			t.Errorf("after feedback the presentation = %+v, want it open with round 2 reviewed", got)
		}
		if again := submitRound(app, second.RoundID, "feedback"); again.Success {
			t.Errorf("submitting round 2 twice = %+v, want it refused", again)
		}

		third := openPresentation(t, cli, repo, "Checkout", checkout.PresentationID)
		if got := presentation(t, app, checkout.PresentationID); got.LatestRoundSeq != 3 || got.LatestRoundSubmitted {
			t.Errorf("after round 3 opened the presentation = %+v, want round 3 awaiting review", got)
		}
		approved := submitRound(app, third.RoundID, "approved", protocol.PresentCommentInput{Filepath: "a.go", LineStart: 1, LineEnd: 1, Side: "new", Content: "nit"})
		if !approved.Success {
			t.Fatalf("approving round 3: %s", protocol.Deref(approved.Error))
		}
		if got := presentation(t, app, checkout.PresentationID); got.Status != "approved" {
			t.Errorf("after approval the presentation = %+v, want approved", got)
		}
		if got := presentationRound(app, checkout.PresentationID, 3); len(got.Comments) != 1 || protocol.Deref(got.Round.Verdict) != "approved" {
			t.Errorf("the approved round = %+v with comments %+v, want its nit kept", got.Round, got.Comments)
		}
		if closed := closePresentation(app, checkout.PresentationID); closed.Success {
			t.Errorf("closing an approved presentation = %+v, want it refused", closed)
		}

		openPresentation(t, cli, repo, "Checkout", checkout.PresentationID)
		if got := presentation(t, app, checkout.PresentationID); got.Status != "open" {
			t.Errorf("after a new round on an approved presentation it is %+v, want it reopened", got)
		}
		if closed := closePresentation(app, checkout.PresentationID); !closed.Success {
			t.Fatalf("closing an open presentation: %s", protocol.Deref(closed.Error))
		}
		if got := presentation(t, app, checkout.PresentationID); got.Status != "closed" {
			t.Errorf("after closing the presentation is %+v, want closed", got)
		}
		if again := closePresentation(app, checkout.PresentationID); again.Success {
			t.Errorf("closing a closed presentation = %+v, want it refused", again)
		}
		openPresentation(t, cli, repo, "Checkout", checkout.PresentationID)
		if got := presentation(t, app, checkout.PresentationID); got.Status != "open" || got.LatestRoundSeq != 5 {
			t.Errorf("after a new round on a closed presentation it is %+v, want it reopened at round 5", got)
		}
	})
}

func openPresentation(t *testing.T, cli *client.Client, repo, title, presentationID string) *protocol.PresentOpenResult {
	t.Helper()
	manifest := fmt.Sprintf("version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: HEAD\n  head: HEAD\n", title, repo)
	opened, err := cli.PresentOpen("presenter", manifest, presentationID)
	if err != nil {
		t.Fatalf("present %s: %v", title, err)
	}
	return opened
}

func presentations(t *testing.T, app *testworld.Peer) []protocol.Presentation {
	t.Helper()
	result := testworld.Request(app, protocol.GetPresentationsMessage{Cmd: protocol.CmdGetPresentations}, protocol.EventGetPresentationsResult,
		func(protocol.GetPresentationsResultMessage) bool { return true })
	if !result.Success {
		t.Fatalf("list presentations: %s", protocol.Deref(result.Error))
	}
	return result.Presentations
}

func presentation(t *testing.T, app *testworld.Peer, id string) protocol.Presentation {
	t.Helper()
	for _, p := range presentations(t, app) {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("presentation %s is not listed", id)
	return protocol.Presentation{}
}

func presentationRound(app *testworld.Peer, presentationID string, seq int) protocol.GetPresentationRoundResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.GetPresentationRoundMessage{Cmd: protocol.CmdGetPresentationRound, PresentationID: presentationID, Seq: protocol.Ptr(seq)},
		protocol.EventGetPresentationRoundResult, func(protocol.GetPresentationRoundResultMessage) bool { return true })
}

func submitRound(app *testworld.Peer, roundID, verdict string, comments ...protocol.PresentCommentInput) protocol.PresentSubmitRoundResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.PresentSubmitRoundMessage{Cmd: protocol.CmdPresentSubmitRound, RoundID: roundID, Verdict: verdict, Comments: comments},
		protocol.EventPresentSubmitRoundResult, func(r protocol.PresentSubmitRoundResultMessage) bool { return r.RoundID == roundID })
}

func closePresentation(app *testworld.Peer, presentationID string) protocol.PresentCloseResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.PresentCloseMessage{Cmd: protocol.CmdPresentClose, PresentationID: presentationID},
		protocol.EventPresentCloseResult, func(r protocol.PresentCloseResultMessage) bool { return r.PresentationID == presentationID })
}

func commentLocations(comments []protocol.PresentationComment) []string {
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		out = append(out, fmt.Sprintf("%s:%d by %s", c.Filepath, c.LineStart, c.Author))
	}
	return out
}
