package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedArtifactsFollowAttachAndDetachNotes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	author := spawnPanes(w, app, w.Path("author"))[0].session
	seed := plantSeedAs(t, cli, author, "Ship the thing")
	plan := gardenArtifactMarkdown("docs/plans/thing.md")
	notebook := &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr("nb-7")}

	gardenArtifactNote(t, cli, author, seed, garden.NoteKindAttach, "", plan)
	if again := gardenArtifactNote(t, cli, author, seed, garden.NoteKindAttach, "", plan); again.Body != "attached docs/plans/thing.md" {
		t.Errorf("the attach note's default body = %q", again.Body)
	}
	gardenArtifactNote(t, cli, author, seed, garden.NoteKindAttach, "the review notes", notebook)
	shown := gardenArtifactShow(t, cli, author, seed)
	if len(shown.References) != 2 || protocol.Deref(shown.References[0].Path) != "docs/plans/thing.md" || protocol.Deref(shown.References[1].NotebookDocumentID) != "nb-7" {
		t.Fatalf("references = %+v, want the markdown file once and the notebook", shown.References)
	}

	for range garden.ShowNotes + 3 {
		if _, err := cli.SeedNote(author, seed, "another day of work", "", "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	shown = gardenArtifactShow(t, cli, author, seed)
	if len(shown.Notes) > garden.ShowNotes || len(shown.References) != 2 {
		t.Fatalf("after a busy log show rendered %d notes and references %+v, want its window and both references", len(shown.Notes), shown.References)
	}

	gardenArtifactNote(t, cli, author, seed, garden.NoteKindDetach, "", plan)
	shown = gardenArtifactShow(t, cli, author, seed)
	if len(shown.References) != 1 || protocol.Deref(shown.References[0].NotebookDocumentID) != "nb-7" {
		t.Errorf("after the detach references = %+v, want the notebook alone", shown.References)
	}
	if want := 4 + garden.ShowNotes + 3; shown.NotesTotal != want {
		t.Errorf("the log holds %d notes, want every attach, detach and note: %d", shown.NotesTotal, want)
	}
}

func TestSeedNotesRefuseArtifactsThatSayNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	author := spawnPanes(w, app, w.Path("author"))[0].session
	seed := plantSeedAs(t, cli, author, "Ship the thing")

	for _, tc := range []struct {
		name, kind, body string
		artifact         *protocol.SeedArtifactReference
		want             string
	}{
		{"an attach with no reference", garden.NoteKindAttach, "here you go", nil, "needs the artifact it attaches"},
		{"a detach with no reference", garden.NoteKindDetach, "never mind", nil, "needs the artifact it detaches"},
		{"a plain note carrying one", garden.NoteKindNote, "look at this", gardenArtifactMarkdown("plan.md"), "a note note carries no artifact"},
		{"a handoff carrying one", garden.NoteKindHandoff, "over to you", gardenArtifactMarkdown("plan.md"), "carries no artifact"},
		{"a malformed reference", garden.NoteKindAttach, "", &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, Path: protocol.Ptr("plan.md")}, "needs notebook_document_id"},
		{"an unknown kind", "bookmark", "x", nil, "is not a kind of note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.SeedNote(author, seed, tc.body, "", tc.kind, false, tc.artifact)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the note = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
	if total := gardenArtifactShow(t, cli, author, seed).NotesTotal; total != 0 {
		t.Errorf("the refused notes wrote %d log entries", total)
	}
}

func gardenArtifactMarkdown(path string) *protocol.SeedArtifactReference {
	return &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr(path)}
}

func gardenArtifactNote(t *testing.T, cli *client.Client, session, seedID, kind, body string, artifact *protocol.SeedArtifactReference) protocol.SeedNote {
	t.Helper()
	result, err := cli.SeedNote(session, seedID, body, "", kind, false, artifact)
	if err != nil {
		t.Fatalf("%s note on %s: %v", kind, seedID, err)
	}
	return result.Note
}

func gardenArtifactShow(t *testing.T, cli *client.Client, session, seedID string) *protocol.SeedShowResult {
	t.Helper()
	shown, err := cli.SeedShow(session, seedID)
	if err != nil {
		t.Fatalf("show %s: %v", seedID, err)
	}
	return shown
}
