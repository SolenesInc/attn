package daemon_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const raceAttempts = 20

const calcNow = "package calc\n\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"

func requestReviewEdit(app *testworld.Peer, document, path, baseHash string, from, to int, text string) protocol.ReviewEditResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return testworld.Request(app, protocol.ReviewEditMessage{Cmd: protocol.CmdReviewEdit, Document: document, Path: path, BaseHash: baseHash, From: from, To: to, Text: text, RequestID: protocol.Ptr(id)},
		protocol.EventReviewEditResult, func(r protocol.ReviewEditResultMessage) bool { return protocol.Deref(r.RequestID) == id })
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func awaitStopShowing(app *testworld.Peer, document, line string) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
		if e.Path != document || e.Review == nil || len(e.Review.Stops) == 0 {
			return false
		}
		for _, row := range e.Review.Stops[0].Rows {
			if row.Text == line {
				return true
			}
		}
		return false
	})
}

func TestAnEditWritesTheFileTheStopShowsItAndUndoRestoresIt(t *testing.T) {
	r := openCalcReview(t)
	calc := filepath.Join(r.repo, "calc.go")
	if err := os.Chmod(calc, 0o755); err != nil {
		t.Fatal(err)
	}
	file := requestReviewFile(r.app, r.document, "calc.go")
	if protocol.Deref(file.Hash) != sha(calcNow) {
		t.Fatalf("review_file hash = %q, want the file's sha256", protocol.Deref(file.Hash))
	}

	edited := requestReviewEdit(r.app, r.document, "calc.go", protocol.Deref(file.Hash), 4, 5, "\ttotal := a + b\n\t// the sum, named\n\treturn total")
	want := "package calc\n\nfunc Add(a, b int) int {\n\ttotal := a + b\n\t// the sum, named\n\treturn total\n}\n"
	if !edited.Success || edited.Conflict || protocol.Deref(edited.Hash) != sha(want) {
		t.Fatalf("the edit answered %+v, want success with the hash of the written file", edited)
	}
	if got := readFile(t, calc); got != want {
		t.Errorf("calc.go after the edit is\n%q\nwant\n%q", got, want)
	}
	if info, err := os.Stat(calc); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("calc.go's mode after the edit is %v (%v), want 0755 kept", info.Mode().Perm(), err)
	}
	shown := awaitStopShowing(r.app, r.document, "\t// the sum, named")
	if stop := shown.Review.Stops[0]; stop.FileHash != sha(want) {
		t.Errorf("the stop after the edit has file hash %q, want the edited file's", stop.FileHash)
	}

	undone := requestReviewEdit(r.app, r.document, "calc.go", protocol.Deref(edited.Hash), 4, 6, "\tsum := a + b\n\treturn sum")
	if !undone.Success || undone.Conflict {
		t.Fatalf("writing the old text back answered %+v, want success", undone)
	}
	if got := readFile(t, calc); got != calcNow {
		t.Errorf("calc.go after the undo is\n%q\nwant the file before the edit\n%q", got, calcNow)
	}
	awaitStopShowing(r.app, r.document, "\treturn sum")
}

func TestAnEditKeepsTheFilesLineEndingsAndReachesFilesUnderDotDirectories(t *testing.T) {
	r := openCalcReview(t)
	workflow := filepath.Join(r.repo, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0o755); err != nil {
		t.Fatal(err)
	}
	crlf := "name: ci\r\non: push\r\njobs: {}\r\n"
	if err := os.WriteFile(workflow, []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}

	edited := requestReviewEdit(r.app, r.document, ".github/workflows/ci.yml", sha(crlf), 2, 2, "on:\n  push: {}")
	want := "name: ci\r\non:\r\n  push: {}\r\njobs: {}\r\n"
	if !edited.Success || edited.Conflict {
		t.Fatalf("the edit of a file under .github answered %+v, want success", edited)
	}
	if got := readFile(t, workflow); got != want {
		t.Errorf("ci.yml after the edit is\n%q\nwant its CRLF endings kept\n%q", got, want)
	}
}

func TestAnEditOfAFileThatChangedUnderneathIsRefusedAndWritesNothing(t *testing.T) {
	r := openCalcReview(t)
	calc := filepath.Join(r.repo, "calc.go")
	file := requestReviewFile(r.app, r.document, "calc.go")
	agentWrote := "package calc\n\n// Add adds.\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"
	if err := os.WriteFile(calc, []byte(agentWrote), 0o644); err != nil {
		t.Fatal(err)
	}

	refused := requestReviewEdit(r.app, r.document, "calc.go", protocol.Deref(file.Hash), 4, 4, "\ttotal := a + b")
	if !refused.Success || !refused.Conflict || protocol.Deref(refused.CurrentHash) != sha(agentWrote) || refused.Hash != nil {
		t.Errorf("the edit of a changed file answered %+v, want a conflict naming the current hash", refused)
	}
	if got := readFile(t, calc); got != agentWrote {
		t.Errorf("calc.go after the refused edit is\n%q\nwant it untouched\n%q", got, agentWrote)
	}

	outside := requestReviewEdit(r.app, r.document, "../calc.go", sha(agentWrote), 1, 1, "x")
	if outside.Success || !strings.Contains(protocol.Deref(outside.Error), "not a path inside the review's repository") {
		t.Errorf("an edit of a path outside the repository answered %+v, want a refusal", outside)
	}

	gitConfig := filepath.Join(r.repo, ".git", "config")
	config := readFile(t, gitConfig)
	inGit := requestReviewEdit(r.app, r.document, ".git/config", sha(config), 1, 1, "x")
	if inGit.Success || !strings.Contains(protocol.Deref(inGit.Error), ".git/config is inside .git") {
		t.Errorf("an edit of a path inside .git answered %+v, want a refusal", inGit)
	}
	if got := readFile(t, gitConfig); got != config {
		t.Error(".git/config was changed by a refused edit")
	}

	large := strings.Repeat("// padding\n", fsdoc.MaxFileSize/11+1)
	if err := os.WriteFile(calc, []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	tooLarge := requestReviewEdit(r.app, r.document, "calc.go", sha(large), 1, 1, "x")
	if tooLarge.Success || !strings.Contains(protocol.Deref(tooLarge.Error), "attn edits files up to 2097152 bytes") {
		t.Errorf("an edit of a file over the limit answered %+v, want a refusal naming the limit", tooLarge)
	}
	if got := readFile(t, calc); got != large {
		t.Error("the file over the limit was changed by a refused edit")
	}
}

func TestASentReviewListsTheUsersEditsApartFromTheNotesWithBothTexts(t *testing.T) {
	r := openCalcReview(t)
	noted := codeMark("n1", "comment", "Return a + b directly.", 7, 5, 6, "    return sum\n}")
	edit := codeMark("e1", "edit", "", 7, 4, 4, "    total := a + b")
	edit.Text = nil
	edit.EditWas = protocol.Ptr("    sum := a + b")
	edit.EditNow = protocol.Ptr("    total := a + b")
	saveDocumentAnnotations(r.app, r.doc, []protocol.MarkdownAnnotation{edit, noted})

	sent := submitReview(r.app, r.doc, r.session, reviewSend{verdict: "approve"})
	if sent.Status != "delivered" || len(sent.Annotations) != 0 {
		t.Fatalf("the send = %+v, want delivered leaving an empty draft", sent)
	}
	got := r.agent.Prompted()
	want := "I've reviewed the change and have 1 piece of feedback:\n\n" +
		"## 1. Note on calc.go:5–6\n```\n    return sum\n}\n```\n> Return a + b directly.\n\n" +
		"---\n\n" +
		"I edited the files in 1 place while reviewing. The edits are saved in the working tree: build on them, and do not undo them.\n\n" +
		"## 2. Edit to calc.go:4\nIt was:\n```\n    sum := a + b\n```\nIt is now:\n```\n    total := a + b\n```\n\n---\nVerdict: approve."
	if !strings.Contains(got, want) {
		t.Errorf("the agent got\n%s\nwant it to list the note, then the edit with both texts:\n%s", got, want)
	}
}

func TestReviewEditIsRefusedWithCodeReviewOff(t *testing.T) {
	r := openCalcReview(t)
	setSetting(t, r.app, "code_review_enabled", "false")
	calc := filepath.Join(r.repo, "calc.go")
	refused := requestReviewEdit(r.app, r.document, "calc.go", sha(calcNow), 4, 4, "\ttotal := a + b")
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "code_review_enabled") {
		t.Errorf("review_edit with code review off answered %+v, want a refusal naming code_review_enabled", refused)
	}
	if got := readFile(t, calc); got != calcNow {
		t.Errorf("calc.go after the refused edit is %q, want it untouched", got)
	}
}

func TestTwoEditsOfAFileAgainstTheSameHashWriteOneAndReportTheOtherAsAConflict(t *testing.T) {
	r := openCalcReview(t)
	notes := filepath.Join(r.repo, "notes.txt")
	base := "first\nsecond\n" + strings.Repeat("padding\n", 4096)
	for attempt := 1; attempt <= raceAttempts; attempt++ {
		if err := os.WriteFile(notes, []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
		first, second := uuid.NewString(), uuid.NewString()
		r.app.Send(protocol.ReviewEditMessage{Cmd: protocol.CmdReviewEdit, Document: r.document, Path: "notes.txt", BaseHash: sha(base), From: 1, To: 1, Text: "FIRST EDIT", RequestID: protocol.Ptr(first)})
		r.app.Send(protocol.ReviewEditMessage{Cmd: protocol.CmdReviewEdit, Document: r.document, Path: "notes.txt", BaseHash: sha(base), From: 2, To: 2, Text: "SECOND EDIT", RequestID: protocol.Ptr(second)})
		answers := map[string]protocol.ReviewEditResultMessage{}
		for _, id := range []string{first, second} {
			answers[id] = testworld.Await(r.app, protocol.EventReviewEditResult, func(e protocol.ReviewEditResultMessage) bool { return protocol.Deref(e.RequestID) == id })
		}
		written := readFile(t, notes)
		var wrote []string
		for id, answer := range answers {
			if !answer.Success {
				t.Fatalf("attempt %d: an edit failed: %+v", attempt, answer)
			}
			if answer.Conflict {
				if protocol.Deref(answer.CurrentHash) != sha(written) {
					t.Fatalf("attempt %d: the conflict names hash %q, want the file's current hash", attempt, protocol.Deref(answer.CurrentHash))
				}
				continue
			}
			wrote = append(wrote, id)
		}
		if len(wrote) != 1 {
			t.Fatalf("attempt %d: %d of the two edits against the same hash reported success, want exactly one; the file holds FIRST EDIT=%v SECOND EDIT=%v",
				attempt, len(wrote), strings.Contains(written, "FIRST EDIT"), strings.Contains(written, "SECOND EDIT"))
		}
	}
}
