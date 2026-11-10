package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/review"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const reviewedFileAtBase = "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"

func reviewRepo(t *testing.T) string {
	t.Helper()
	repo := newRepo(t, "calc")
	commitFile(t, repo, "calc.go", reviewedFileAtBase)
	if err := os.WriteFile(filepath.Join(repo, "calc.go"), []byte("package calc\n\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func appendReviewBody(t *testing.T, document, body string) {
	t.Helper()
	frontmatter, err := os.ReadFile(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, append(frontmatter, body...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func reviewRows(stop protocol.ReviewStop) []string {
	rows := make([]string, 0, len(stop.Rows))
	for _, row := range stop.Rows {
		rows = append(rows, row.Kind+row.Text)
	}
	return rows
}

func awaitDocumentContent(app *testworld.Peer, path string) protocol.WebSocketEvent {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.Path) == path
	})
}

func TestAReviewDocumentReachesItsTileWithTheStopsRowsAndFollowsTheirFiles(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "code_review_enabled", "true")
		repo := reviewRepo(t)
		document := w.Path("reviews", "calc.md")

		started, err := cli.ReviewStart(document, repo, "HEAD")
		if err != nil {
			t.Fatalf("review start: %v", err)
		}
		if !started.Created || started.Base == nil || started.Head == "" {
			t.Fatalf("review start answered %+v, want a created document with a base and a head", started)
		}
		appendReviewBody(t, document, "# Name the sum\n\n## Add keeps its result\n\n### calc.go:3-6\n\n- L4!: The sum gets a name.\n")

		problems, err := cli.OpenMarkdown(document, "s1")
		if err != nil || len(problems) != 0 {
			t.Fatalf("opening the review answered problems %+v, error %v; want none", problems, err)
		}
		opened := awaitDocumentContent(app, document)
		if !strings.Contains(protocol.Deref(opened.Content), "### calc.go:3-6") {
			t.Errorf("the tile got content %q, want the document as written", protocol.Deref(opened.Content))
		}
		if opened.Review == nil || len(opened.Review.Stops) != 1 {
			t.Fatalf("the bundle has stops %+v, want one", opened.Review.Stops)
		}
		stop := opened.Review.Stops[0]
		if got, want := reviewRows(stop), []string{" func Add(a, b int) int {", "-\treturn a + b", "+\tsum := a + b", "+\treturn sum", " }"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("the stop's rows are %q, want %q", got, want)
		}
		if len(stop.Notes) != 1 || !stop.Notes[0].Look || stop.Notes[0].NowFrom != 4 {
			t.Errorf("the stop's notes are %+v, want the look note at line 4", stop.Notes)
		}

		w.advance(time.Second)
		mark := len(app.Received())
		if err := os.WriteFile(filepath.Join(repo, "calc.go"), []byte("package calc\n\n// Add adds.\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.advance(time.Second)
		pushed := markdownTileContents(app, mark, "")
		if len(pushed) != 1 || pushed[0].Review == nil || len(pushed[0].Review.Stops) != 1 {
			t.Fatalf("after the stop's file changed the tile got %v, want one new bundle", markdownTileBodies(pushed))
		}
		moved := pushed[0].Review.Stops[0]
		if moved.NowFrom != 4 || moved.NowTo != 7 || moved.From != 3 || moved.ChangedSincePresented {
			t.Errorf("after a line was added above it the stop is %d-%d now (written %d-%d, changed %v), want 4-7 now, 3-6 as written, unchanged",
				moved.NowFrom, moved.NowTo, moved.From, moved.To, moved.ChangedSincePresented)
		}
	})
}

func TestAReviewDocumentsProblemsReachWhoeverOpensOrChecksIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := reviewRepo(t)
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Past the end\n\n## Add\n\n### calc.go:3-40\n")

	checked, err := cli.ReviewCheck(document)
	if err != nil {
		t.Fatalf("review check: %v", err)
	}
	opened, err := cli.OpenMarkdown(document, "s1")
	if err != nil {
		t.Fatalf("opening a review with a problem failed instead of opening: %v", err)
	}
	want := "the stop calc.go:3-40 ends past the file (6 lines at head). Use 3-6."
	if len(checked) != 1 || checked[0].Message != want || checked[0].Line != 12 {
		t.Errorf("review check answered %+v, want line 12: %q", checked, want)
	}
	if len(opened) != 1 || opened[0] != checked[0] {
		t.Errorf("open_markdown answered problems %+v, want what review check answered, %+v", opened, checked)
	}
	tile := awaitDocumentContent(app, document)
	if tile.Review == nil || len(tile.Review.Stops) != 1 || tile.Review.Stops[0].To != 6 {
		t.Errorf("the tile's bundle is %+v, want the clipped stop 3-6", tile.Review)
	}
}

func requestReviewFile(app *testworld.Peer, document, path string) protocol.ReviewFileResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return testworld.Request(app, protocol.ReviewFileMessage{Cmd: protocol.CmdReviewFile, Document: document, Path: path, RequestID: protocol.Ptr(id)},
		protocol.EventReviewFileResult, func(r protocol.ReviewFileResultMessage) bool { return protocol.Deref(r.RequestID) == id })
}

func requestReviewSearch(app *testworld.Peer, document, query string) protocol.ReviewSearchResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return testworld.Request(app, protocol.ReviewSearchMessage{Cmd: protocol.CmdReviewSearch, Document: document, Query: query, RequestID: protocol.Ptr(id)},
		protocol.EventReviewSearchResult, func(r protocol.ReviewSearchResultMessage) bool { return protocol.Deref(r.RequestID) == id })
}

func TestTheFileViewReadsAWholeFileOfTheReviewAndSearchFindsItsFilesAndLines(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "code_review_enabled", "true")
	repo := reviewRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("the sum of two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD"); err != nil {
		t.Fatalf("review start: %v", err)
	}

	file := requestReviewFile(app, document, "calc.go")
	if !file.Success {
		t.Fatalf("review_file failed: %s", protocol.Deref(file.Error))
	}
	var rows []string
	for _, row := range file.Rows {
		rows = append(rows, fmt.Sprintf("%s%d|%d %s", row.Kind, protocol.Deref(row.Old), protocol.Deref(row.Now), row.Text))
	}
	want := []string{" 1|1 package calc", " 2|2 ", " 3|3 func Add(a, b int) int {", "-4|0 \treturn a + b", "+0|4 \tsum := a + b", "+0|5 \treturn sum", " 5|6 }"}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("review_file rows are\n%s\nwant the whole file now with the change from base\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if protocol.Deref(file.Hash) == "" {
		t.Error("review_file answered no hash for a file that exists")
	}

	outside := requestReviewFile(app, document, "../elsewhere.go")
	if outside.Success || !strings.Contains(protocol.Deref(outside.Error), repo) {
		t.Errorf("review_file of a path outside the repository answered %+v, want a refusal naming the repository %s", outside, repo)
	}

	byName := requestReviewSearch(app, document, "CALC")
	if !byName.Success || strings.Join(byName.Files, ",") != "calc.go" {
		t.Errorf("searching CALC found files %v (error %s), want calc.go by name", byName.Files, protocol.Deref(byName.Error))
	}
	byText := requestReviewSearch(app, document, "sum")
	var hits []string
	for _, hit := range byText.Hits {
		hits = append(hits, fmt.Sprintf("%s:%d:%s", hit.Path, hit.Line, hit.Text))
	}
	if want := []string{"calc.go:4:\tsum := a + b", "calc.go:5:\treturn sum", "notes.txt:1:the sum of two"}; strings.Join(hits, "\n") != strings.Join(want, "\n") {
		t.Errorf("searching sum found %q, want the lines now, untracked files included: %q", hits, want)
	}

	if err := os.WriteFile(filepath.Join(repo, "crowded.txt"), []byte(strings.Repeat("crowd\n", review.MaxSearchHitsInFile+5)), 0o644); err != nil {
		t.Fatal(err)
	}
	crowded := requestReviewSearch(app, document, "crowd")
	if len(crowded.Hits) != review.MaxSearchHitsInFile || !strings.Contains(protocol.Deref(crowded.Problem), "crowded.txt match on more than") {
		t.Errorf("searching a word on %d lines of one file found %d hits with problem %q, want %d and the file named", review.MaxSearchHitsInFile+5, len(crowded.Hits), protocol.Deref(crowded.Problem), review.MaxSearchHitsInFile)
	}

	for i := range review.MaxSearchHits/review.MaxSearchHitsInFile + 2 {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("common%02d.txt", i)), []byte(strings.Repeat("common\n", review.MaxSearchHitsInFile)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	common := requestReviewSearch(app, document, "common")
	if len(common.Hits) != review.MaxSearchHits || !strings.Contains(protocol.Deref(common.Problem), fmt.Sprintf("over %d lines match", review.MaxSearchHits)) {
		t.Errorf("searching a word on more than %d lines found %d hits with problem %q, want %d and the limit named", review.MaxSearchHits, len(common.Hits), protocol.Deref(common.Problem), review.MaxSearchHits)
	}
}

func TestTheFileViewFollowsARenameTheReviewSnapshotHoldsLikeItsStop(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "code_review_enabled", "true")
		repo := newRepo(t, "calc")
		commitFile(t, repo, "calc.go", reviewedFileAtBase)
		runGit(t, repo, "mv", "calc.go", "math.go")
		if err := os.WriteFile(filepath.Join(repo, "math.go"), []byte("package calc\n\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		document := w.Path("reviews", "calc.md")
		if _, err := cli.ReviewStart(document, repo, "HEAD"); err != nil {
			t.Fatalf("review start: %v", err)
		}
		appendReviewBody(t, document, "# Rename\n\n## Add keeps its result\n\n### math.go:3-6\n")
		if problems, err := cli.OpenMarkdown(document, "s1"); err != nil || len(problems) != 0 {
			t.Fatalf("opening the review answered problems %+v, error %v; want none", problems, err)
		}
		opened := awaitDocumentContent(app, document)
		if opened.Review == nil || len(opened.Review.Stops) != 1 {
			t.Fatalf("the bundle has stops %+v, want one", opened.Review)
		}

		file := requestReviewFile(app, document, "math.go")
		if !file.Success {
			t.Fatalf("review_file failed: %s", protocol.Deref(file.Error))
		}
		var rows []string
		for _, row := range file.Rows {
			rows = append(rows, row.Kind+row.Text)
		}
		want := []string{" package calc", " ", " func Add(a, b int) int {", "-\treturn a + b", "+\tsum := a + b", "+\treturn sum", " }"}
		if strings.Join(rows, "\n") != strings.Join(want, "\n") {
			t.Errorf("review_file rows of the renamed file are\n%q\nwant its change from base, unchanged lines as context\n%q", rows, want)
		}
		if stopRows := reviewRows(opened.Review.Stops[0]); strings.Join(rows[2:], "\n") != strings.Join(stopRows, "\n") {
			t.Errorf("the file view shows %q for the stop's lines, the stop shows %q; want the same rows", rows[2:], stopRows)
		}
	})
}

func TestWithCodeReviewOffAReviewDocumentIsPlainMarkdownAndReviewRefuses(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	repo := reviewRepo(t)
	document := w.Path("reviews", "calc.md")
	if err := os.MkdirAll(filepath.Dir(document), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nattn: review\nframe: brief\nrepository: " + repo + "\nhead: HEAD\n---\n# Add\n\n### calc.go:3-40\n"
	if err := os.WriteFile(document, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, refused := range map[string]func() error{
		"review start": func() error { _, err := cli.ReviewStart(document, repo, "HEAD"); return err },
		"review check": func() error { _, err := cli.ReviewCheck(document); return err },
		"nav status":   func() error { _, err := cli.CodeNavStatus(repo); return err },
		"nav check":    func() error { _, err := cli.CodeNavCheck(repo, "calc.go", 3); return err },
		"nav link":     func() error { _, err := cli.CodeNavLink("/bin/sh"); return err },
		"nav unlink":   func() error { return cli.CodeNavUnlink("sh") },
		"nav set": func() error {
			_, err := cli.CodeNavDefine(repo, "zig", `{"command":["zls"],"extensions":[".zig"]}`)
			return err
		},
	} {
		if err := refused(); err == nil || !strings.Contains(err.Error(), "code_review_enabled") {
			t.Errorf("%s with code review off answered %v, want a refusal naming code_review_enabled", name, err)
		}
	}
	if file := requestReviewFile(app, document, "calc.go"); file.Success || !strings.Contains(protocol.Deref(file.Error), "code_review_enabled") {
		t.Errorf("review_file with code review off answered %+v, want a refusal naming code_review_enabled", file)
	}
	if search := requestReviewSearch(app, document, "sum"); search.Success || !strings.Contains(protocol.Deref(search.Error), "code_review_enabled") {
		t.Errorf("review_search with code review off answered %+v, want a refusal naming code_review_enabled", search)
	}
	if nav := requestCodeNav(app, document, "calc.go", 3, 5, "definition"); nav.Success || !strings.Contains(protocol.Deref(nav.Error), "code_review_enabled") {
		t.Errorf("code_nav with code review off answered %+v, want a refusal naming code_review_enabled", nav)
	}
	if setup := requestCodeNavSetup(app, protocol.CodeNavSetupMessage{DesktopID: "desktop_1", Document: document, Repository: repo, Language: "go", File: "calc.go", State: "missing"}); setup.Success || !strings.Contains(protocol.Deref(setup.Error), "code_review_enabled") {
		t.Errorf("code_nav_setup with code review off answered %+v, want a refusal naming code_review_enabled", setup)
	}
	problems, err := cli.OpenMarkdown(document, "s1")
	if err != nil || len(problems) != 0 {
		t.Fatalf("opening the document answered problems %+v, error %v; want it opened with none", problems, err)
	}
	tile := awaitDocumentContent(app, document)
	if protocol.Deref(tile.Content) != body || tile.Review != nil {
		t.Errorf("the tile got content %q and review %+v, want the document as written and no review", protocol.Deref(tile.Content), tile.Review)
	}
}
