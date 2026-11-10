package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	} {
		if err := refused(); err == nil || !strings.Contains(err.Error(), "code_review_enabled") {
			t.Errorf("%s with code review off answered %v, want a refusal naming code_review_enabled", name, err)
		}
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
