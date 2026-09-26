package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPresentationGrowsRoundsOnlyForTheSessionThatOpenedIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")

	opened := openPresentation(t, cli, repo, "My Change", "")
	if opened.Seq != 1 || opened.Title != "My Change" || len(opened.BaseSHA) != 40 || len(opened.HeadSHA) != 40 {
		t.Fatalf("a new presentation = %+v, want round 1 of My Change pinned to full SHAs", opened)
	}
	if listed := presentation(t, app, opened.PresentationID); listed.SessionID != "presenter" || listed.Title != "My Change" {
		t.Errorf("listed presentation = %+v, want My Change from presenter", listed)
	}
	if again := openPresentation(t, cli, repo, "My Change", opened.PresentationID); again.PresentationID != opened.PresentationID || again.Seq != 2 {
		t.Fatalf("reopening %s = %+v, want round 2 of it", opened.PresentationID, again)
	}

	for _, tc := range []struct {
		name, session, manifest, presentationID string
	}{
		{"a malformed manifest", "presenter", "not: valid: manifest: yaml: [", ""},
		{"an unknown presentation", "presenter", presentationManifest("My Change", repo, "HEAD", "HEAD", ""), "no-such-presentation"},
		{"another session's presentation", "someone-else", presentationManifest("My Change", repo, "HEAD", "HEAD", ""), opened.PresentationID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if refused, err := cli.PresentOpen(tc.session, tc.manifest, tc.presentationID); err == nil {
				t.Errorf("presenting %s = %+v, want it refused", tc.name, refused)
			}
		})
	}
	if got := presentation(t, app, opened.PresentationID); got.LatestRoundSeq != 2 || got.SessionID != "presenter" {
		t.Errorf("after the refusals the presentation = %+v, want presenter's with round 2 latest", got)
	}
}

func TestAReviewRoundRefusesBadCommentsAndFeedbackReportsItsOutcome(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	checkout := openPresentation(t, cli, repo, "Checkout", "")

	for _, tc := range []struct {
		name    string
		comment protocol.PresentCommentInput
	}{
		{"a side that is neither old nor new", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "sideways", Content: "x"}},
		{"a line before the first", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 0, LineEnd: 1, Side: "new", Content: "x"}},
		{"a range that ends before it starts", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 5, LineEnd: 1, Side: "new", Content: "x"}},
		{"blank content", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "   "}},
		{"a blank path", protocol.PresentCommentInput{Filepath: "   ", LineStart: 1, LineEnd: 1, Side: "new", Content: "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if refused := submitRound(app, checkout.RoundID, "feedback", tc.comment); refused.Success {
				t.Errorf("submitting a comment with %s = %+v, want it refused", tc.name, refused)
			}
		})
	}
	if got := presentationRound(app, checkout.PresentationID, 1); got.Round.SubmittedAt != nil {
		t.Errorf("round 1 after the refusals = %+v, want it still awaiting review", got.Round)
	}
	expectPresentationFeedback(t, w, checkout.PresentationID, false, "", "open")

	if approved := submitRound(app, checkout.RoundID, "approved", protocol.PresentCommentInput{Filepath: "a.txt", LineStart: 1, LineEnd: 1, Side: "new", Content: "nit"}); !approved.Success {
		t.Fatalf("approving round 1: %s", protocol.Deref(approved.Error))
	}
	expectPresentationFeedback(t, w, checkout.PresentationID, true, "approved", "approved")

	pricing := openPresentation(t, cli, repo, "Pricing", "")
	if closed := closePresentation(app, pricing.PresentationID); !closed.Success || closed.PresentationID != pricing.PresentationID {
		t.Fatalf("closing %s = %+v", pricing.PresentationID, closed)
	}
	if got := presentationRound(app, pricing.PresentationID, 1); got.Round.SubmittedAt != nil {
		t.Errorf("the closed presentation's round = %+v, want it never submitted", got.Round)
	}
	expectPresentationFeedback(t, w, pricing.PresentationID, false, "", "closed")
	if unnamed := closePresentation(app, ""); unnamed.Success {
		t.Errorf("closing without a presentation id = %+v, want it refused", unnamed)
	}
}

func TestPresentationAnchorsResolveToLinesWarnWhenAmbiguousAndRefuseWhenMissing(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	commitFile(t, repo, "a.txt", "package a\nfunc Foo() {\n  // TODO: fix\n  return\n}\n// TODO: also fix\n")
	annotated := func(anchor, note string) string {
		return presentationManifest("Annotated", repo, "HEAD", "HEAD",
			fmt.Sprintf("files:\n  - path: a.txt\n    annotations:\n      - anchor: %q\n        note: %q\n", anchor, note))
	}

	if _, err := cli.PresentOpen("presenter", annotated("does not exist", "nope"), ""); err == nil || !strings.Contains(err.Error(), "a.txt[0]") {
		t.Errorf("presenting an anchor that is not in the file = %v, want it refused naming a.txt[0]", err)
	}
	ambiguous, err := cli.PresentOpen("presenter", annotated("TODO", "which one"), "")
	if err != nil {
		t.Fatalf("presenting an ambiguous anchor: %v", err)
	}
	if len(ambiguous.Warnings) != 1 || !strings.Contains(ambiguous.Warnings[0], "a.txt[0]") {
		t.Errorf("warnings for an ambiguous anchor = %q, want one naming a.txt[0]", ambiguous.Warnings)
	}

	resolved, err := cli.PresentOpen("presenter", annotated("func Foo", "entry point"), "")
	if err != nil {
		t.Fatalf("presenting a resolvable anchor: %v", err)
	}
	round := presentationRound(app, resolved.PresentationID, 1)
	if notes := presentedFile(t, round, "a.txt").Annotations; len(notes) != 1 || notes[0].LineStart != 2 || notes[0].LineEnd != 2 || !slices.Equal(notes[0].Comments, []string{"entry point"}) {
		t.Errorf("a.txt annotations = %+v, want line 2 carrying the note", notes)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if notes := presentedFile(t, presentationRound(app, resolved.PresentationID, 1), "a.txt").Annotations; notes != nil {
		t.Errorf("a.txt annotations once the repo is gone = %+v, want none while the round still loads", notes)
	}
}

func TestAPresentationRoundDescribesTheChangeItFrames(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	write := func(name string, body []byte) {
		if err := os.WriteFile(filepath.Join(repo, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", []byte("line1\nline2\n"))
	write("img.png", []byte{0x89, 0x00, 0x50, 0x4e})
	write("old.txt", []byte("one\ntwo\nthree\nfour\nfive\n"))
	runGit(t, repo, "add", "a.txt", "img.png", "old.txt")
	runGit(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	write("a.txt", []byte("line1\nline2\nline3\nline4\n"))
	write("b.txt", []byte("new file\n"))
	write("img.png", []byte{0x89, 0x00, 0x50, 0x4e, 0x01, 0x02})
	runGit(t, repo, "mv", "old.txt", "renamed.txt")
	runGit(t, repo, "add", "a.txt", "b.txt", "img.png")
	runGit(t, repo, "commit", "-m", "head")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	later := commitFile(t, repo, "c.txt", "after the frame\n")

	opened, err := cli.PresentOpen("presenter", presentationManifest("Stats", repo, base, head, "files:\n  - path: a.txt\n  - path: img.png\n"), "")
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	round := presentationRound(app, opened.PresentationID, 1)
	if !round.Success || protocol.Deref(round.RepoHeadSHA) != later {
		t.Fatalf("round = %+v with repo head %q, want it loaded with the repo's current head %s", round.Round, protocol.Deref(round.RepoHeadSHA), later)
	}
	if got := presentedFileStats(presentedFile(t, round, "a.txt")); got != "+2/-0" {
		t.Errorf("a.txt in the manifest shows %s, want +2/-0", got)
	}
	if got := presentedFileStats(presentedFile(t, round, "img.png")); got != "none" {
		t.Errorf("the binary img.png in the manifest shows %s, want no stats", got)
	}
	changed := map[string]string{}
	for _, f := range round.Round.ChangedFiles {
		changed[f.Path] = presentedFileStats(f)
	}
	for path, want := range map[string]string{"a.txt": "+2/-0", "b.txt": "+1/-0", "img.png": "none", "renamed.txt": "none"} {
		if got, ok := changed[path]; !ok || got != want {
			t.Errorf("changed file %s = %q (listed %v), want %s", path, got, ok, want)
		}
	}
	if _, ok := changed["old.txt"]; ok {
		t.Errorf("changed files = %v, want the renamed file only under its new path", changed)
	}
	if _, ok := changed["c.txt"]; ok {
		t.Errorf("changed files = %v, want only the frame's changes", changed)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if gone := presentationRound(app, opened.PresentationID, 1); !gone.Success || gone.Round.ChangedFiles != nil {
		t.Errorf("once the repo is gone the round = %+v, want it loaded without changed files", gone.Round)
	}
}

func TestSubmittingARoundHandsItBackToThePresenterOnceItIsIdle(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("present the checkout change")
	})
	agent := w.Launched(session)
	agent.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	opened, err := cli.PresentOpen(session, presentationManifest("Checkout", repo, "HEAD", "HEAD", ""), "")
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	submitted := testworld.Request(app, protocol.PresentSubmitRoundMessage{Cmd: protocol.CmdPresentSubmitRound, RoundID: opened.RoundID, Verdict: "approved", Handback: true},
		protocol.EventPresentSubmitRoundResult, func(r protocol.PresentSubmitRoundResultMessage) bool { return r.RoundID == opened.RoundID })
	if !submitted.Success {
		t.Fatalf("approving the round: %s", protocol.Deref(submitted.Error))
	}
	agent.Reply("Presented the checkout change. <!-- attn:state=idle -->")
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the presenter was prompted with %q, want the inbox doorbell", got)
	}
	inbox := readInbox(t, cli, session, 0)
	if len(inbox.Items) != 1 || !strings.Contains(inbox.Items[0].Content, "attn present feedback") {
		t.Fatalf("the presenter's inbox = %+v, want one handback telling it to run attn present feedback", inbox.Items)
	}
	notified, err := time.Parse(time.RFC3339Nano, inbox.Items[0].NotifiedAt)
	if err != nil {
		t.Fatalf("notified_at %q: %v", inbox.Items[0].NotifiedAt, err)
	}
	if since := stateSince(t, idle); notified.Before(since) {
		t.Errorf("the doorbell rang at %s, while the presenter was still working; it went idle at %s", notified, since)
	}
}

func presentationManifest(title, repo, base, head, files string) string {
	return fmt.Sprintf("version: 1\nkind: changes\ntitle: %q\nframe:\n  repo: %q\n  base: %q\n  head: %q\n%s", title, repo, base, head, files)
}

func presentedFile(t *testing.T, round protocol.GetPresentationRoundResultMessage, path string) protocol.PresentFile {
	t.Helper()
	if !round.Success || round.Round == nil {
		t.Fatalf("round = %+v, want it loaded", round)
	}
	for _, f := range round.Round.Manifest.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("%s is not in the round's manifest: %+v", path, round.Round.Manifest.Files)
	return protocol.PresentFile{}
}

func presentedFileStats(f protocol.PresentFile) string {
	if f.Additions == nil && f.Deletions == nil {
		return "none"
	}
	return fmt.Sprintf("+%d/-%d", protocol.Deref(f.Additions), protocol.Deref(f.Deletions))
}

func expectPresentationFeedback(t *testing.T, w *world, presentationID string, submitted bool, verdict, status string) {
	t.Helper()
	got, err := w.Client().PresentFeedback(presentationID, 0)
	if err != nil {
		t.Fatalf("present feedback for %s: %v", presentationID, err)
	}
	if got.Submitted != submitted || protocol.Deref(got.Verdict) != verdict || (verdict == "" && got.Verdict != nil) || got.PresentationStatus != status {
		t.Errorf("feedback = submitted %v, verdict %v, status %s; want submitted %v, verdict %q, status %s",
			got.Submitted, got.Verdict, got.PresentationStatus, submitted, verdict, status)
	}
}
