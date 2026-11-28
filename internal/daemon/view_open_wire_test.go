package daemon_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const transcriptView = `# Check the transcripts

:::data file="team.json" as="team"
:::

:::facts
Checked by | {team.lead}
:::

:::list team.jsonl
title: {name}
:::

::::one-by-one clips.jsonl
![{id}]({file})
::::
`

func writeViewFolder(t *testing.T, folder string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func viewValues(view *protocol.ViewDocument) map[string]string {
	values := map[string]string{}
	for _, v := range view.Values {
		encoded, _ := json.Marshal(v.Values)
		values[v.Source] = string(encoded)
	}
	return values
}

func viewSourceStates(view *protocol.ViewDocument) map[string]string {
	states := map[string]string{}
	for _, source := range view.Sources {
		states[source.ID] = string(source.State) + " " + protocol.Deref(source.Notice)
	}
	return states
}

func TestAViewReachesItsTileWithTheValuesOfItsFilesAndFollowsThem(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "views_enabled", "true")
		folder := w.Path("check")
		view := filepath.Join(folder, "view.md")
		writeViewFolder(t, folder, map[string]string{
			"view.md":     transcriptView,
			"team.json":   `{"lead": "Ana"}`,
			"clips.jsonl": "{\"id\": \"clip-01\", \"file\": \"clip-01.png\"}\n{\"id\": \"clip-02\", \"file\": \"clip-02.png\"}\n",
		})

		problems, err := cli.OpenMarkdown(view, "s1")
		if err != nil || len(problems) != 0 {
			t.Fatalf("opening the view answered problems %+v, error %v; want none", problems, err)
		}
		opened := awaitDocumentContent(app, view)
		if opened.View == nil {
			t.Fatalf("the tile got no view, only content %q", protocol.Deref(opened.Content))
		}
		var names []string
		for _, part := range opened.View.Parts {
			names = append(names, part.Name)
		}
		if got, want := strings.Join(names, " "), "markdown data facts list one-by-one"; got != want {
			t.Errorf("the view's parts are %q, want %q", got, want)
		}
		if got, want := viewValues(opened.View), map[string]string{
			"team.json":   `{"lead":"Ana"}`,
			"clips.jsonl": `[{"file":"clip-01.png","id":"clip-01"},{"file":"clip-02.png","id":"clip-02"}]`,
		}; !maps.Equal(got, want) {
			t.Errorf("the view's values are %v, want %v", got, want)
		}
		if got := viewSourceStates(opened.View)["team.jsonl"]; !strings.HasPrefix(got, "failed team.jsonl: file not found") {
			t.Errorf("the missing team.jsonl is %q, want failed with its notice", got)
		}

		w.advance(time.Second)
		mark := len(app.Received())
		writeViewFolder(t, folder, map[string]string{"clips.jsonl": "{\"id\": \"clip-03\", \"file\": \"clip-03.png\"}\n"})
		w.advance(time.Second)
		pushed := markdownTileContents(app, mark, "")
		if len(pushed) != 1 || pushed[0].View == nil {
			t.Fatalf("after a data file changed the tile got %v, want one new view", markdownTileBodies(pushed))
		}
		if got := viewValues(pushed[0].View)["clips.jsonl"]; got != `[{"file":"clip-03.png","id":"clip-03"}]` {
			t.Errorf("after clips.jsonl changed its values are %s, want the new clip", got)
		}
	})
}

func TestOpenAnswersAViewsProblemsAndTheBrokenPartCarriesItsOwn(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "views_enabled", "true")
	folder := w.Path("broken")
	view := filepath.Join(folder, "view.md")
	writeViewFolder(t, folder, map[string]string{"view.md": "# Broken\n\n:::lst rows\n:::\n\n:::data file=\"../secrets.json\"\n:::\n\n:::status state\n:::\n"})

	problems, err := cli.OpenMarkdown(view, "s1")
	if err != nil {
		t.Fatalf("opening a view with problems failed instead of opening: %v", err)
	}
	var lines []int
	for _, problem := range problems {
		lines = append(lines, int(problem.Line))
	}
	if len(problems) != 2 || lines[0] != 3 || lines[1] != 6 ||
		!strings.Contains(problems[0].Message, `Did you mean "list"?`) ||
		!strings.Contains(problems[1].Message, `"../secrets.json" is outside the view's folder`) {
		t.Fatalf("open answered problems %+v, want line 3 naming list and line 6 naming the folder", problems)
	}
	tile := awaitDocumentContent(app, view)
	if tile.View == nil || len(tile.View.Problems) != 2 {
		t.Fatalf("the tile's view is %+v, want its two problems", tile.View)
	}
	placed := map[string]bool{}
	for _, part := range tile.View.Parts {
		placed[part.Name] = part.Problem != nil
	}
	if !placed["data"] || placed["status"] {
		t.Errorf("parts with a problem: %v; want the data part to carry its own and status none", placed)
	}
}

func TestWithViewsOffAViewIsPlainMarkdownAndOpenAnswersNoProblems(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	folder := w.Path("check")
	view := filepath.Join(folder, "view.md")
	writeViewFolder(t, folder, map[string]string{"view.md": transcriptView + "\n:::lst\n:::\n"})

	problems, err := cli.OpenMarkdown(view, "s1")
	if err != nil || len(problems) != 0 {
		t.Fatalf("opening the view with views off answered problems %+v, error %v; want none", problems, err)
	}
	tile := awaitDocumentContent(app, view)
	if tile.View != nil || protocol.Deref(tile.Content) != transcriptView+"\n:::lst\n:::\n" {
		t.Errorf("the tile got view %+v and content %q, want the file as written and no view", tile.View, protocol.Deref(tile.Content))
	}
}

func TestWithViewsOnMarkdownThatOnlyLooksLikeAViewOpensAsWritten(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "views_enabled", "true")
	folder := w.Path("notes")
	notes := filepath.Join(folder, "notes.md")
	written := "# Notes\n\n:::note\nKeep this.\n:::\n\n```markdown\n:::list ci.jobs\n:::\n```\n"
	writeViewFolder(t, folder, map[string]string{"notes.md": written})

	problems, err := cli.OpenMarkdown(notes, "s1")
	if err != nil || len(problems) != 0 {
		t.Fatalf("opening the notes answered problems %+v, error %v; want none", problems, err)
	}
	tile := awaitDocumentContent(app, notes)
	if tile.View != nil || protocol.Deref(tile.Content) != written {
		t.Errorf("the tile got view %+v and content %q, want the file as written and no view", tile.View, protocol.Deref(tile.Content))
	}
}
