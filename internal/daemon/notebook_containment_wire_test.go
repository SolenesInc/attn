package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTheNotebookStaysInsideItsRootEvenWhenTheRootIsALink(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	real := fsDir(t, "real-notebook")
	root := filepath.Join(w.Dir, "notebook")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, root); err != nil {
		t.Fatal(err)
	}
	outside := fsDir(t, "outside")
	secret := filepath.Join(outside, "secret.md")
	fsWriteFile(t, secret, []byte(notebookNote("TOP SECRET")))
	for link, target := range map[string]string{"evil": outside, "linked.md": secret} {
		if err := os.Symlink(target, filepath.Join(real, link)); err != nil {
			t.Fatal(err)
		}
	}

	if written := notebookAskWrite(app, "knowledge/areas/foo.md", notebookNote("inside"), ""); !written.Success || written.Result == nil || written.Result.Conflict {
		t.Fatalf("writing under a linked notebook root = %+v (%s), want it saved", written.Result, protocol.Deref(written.Error))
	}
	if _, err := os.Stat(filepath.Join(real, "knowledge", "areas", "foo.md")); err != nil {
		t.Errorf("the write did not land in the directory the root links to: %v", err)
	}
	notebookAskWrite(app, "knowledge/areas-archive/bar.md", notebookNote("sibling"), "")

	if read := notebookAskRead(app, "evil/secret.md"); read.Success || (read.Result != nil && strings.Contains(read.Result.Content, "TOP SECRET")) {
		t.Errorf("reading through a link out of the notebook = %+v, want a refusal", read.Result)
	}
	if written := notebookAskWrite(app, "evil/planted.md", notebookNote("planted"), ""); written.Success {
		t.Errorf("writing through a link out of the notebook = %+v, want a refusal", written.Result)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.md")); err == nil {
		t.Error("a notebook write landed outside the notebook")
	}
	if listed := notebookEntryPaths(notebookAskList(app, "").Entries); !slices.Equal(listed, []string{"knowledge/areas-archive/bar.md", "knowledge/areas/foo.md"}) {
		t.Errorf("listing the notebook = %v, want only the notes inside it", listed)
	}
	if areas := notebookEntryPaths(notebookAskList(app, "knowledge/areas").Entries); !slices.Equal(areas, []string{"knowledge/areas/foo.md"}) {
		t.Errorf("listing knowledge/areas = %v, want its own notes and not its sibling's", areas)
	}
}

func TestAJournalAppendKeepsTheNotesOwnFrontmatter(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	fsNotebookRoot(t, w)
	existing := "---\ntype: journal\n# external note\nobsidian_id: 007\ntitle: jrnl\n---\n# entries\n\nfirst\n"
	notebookAskWrite(app, "journal/2026-06-13.md", existing, "")
	if _, err := cli.AppendJournal("", "2026-06-13", "second"); err != nil {
		t.Fatal(err)
	}
	read := notebookAskRead(app, "journal/2026-06-13.md")
	if read.Result == nil {
		t.Fatalf("reading the journal: %s", protocol.Deref(read.Error))
	}
	for _, want := range []string{"# external note", "obsidian_id: 007", "title: jrnl", "first", "second"} {
		if !strings.Contains(read.Result.Content, want) {
			t.Errorf("appending dropped %q from the journal:\n%s", want, read.Result.Content)
		}
	}
}
