package daemon_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func TestSeedBodyEditsRefuseLostWritesAndKeepRollbackHistory(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	const original = "# Work\n\nKeep trailing whitespace.  \n\n"
	planted, err := cli.SeedPlant("", "Body history", original, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := planted.Seed.ID
	if _, err := cli.SeedRollback(id, nil); err == nil {
		t.Fatal("a seed with no edits offered a rollback")
	}
	base := garden.BodySHA256(original)
	if _, err := cli.SeedNote("", id, "A note changes the revision, not the body.", "", false, nil); err != nil {
		t.Fatal(err)
	}
	other := original + "## Coordination\n\nAnother writer's change.\n"
	first, err := cli.SeedEdit(id, other, base, "add coordination")
	if err != nil {
		t.Fatal(err)
	}
	if first.BodyVersion != 2 || first.PreviousBytes != len(original) || first.NewBytes != len(other) {
		t.Fatalf("first edit receipt: %+v", first)
	}
	if _, err := cli.SeedEdit(id, "stale replacement", base, ""); err == nil {
		t.Fatal("accepted a stale replacement")
	}
	for _, body := range []string{"", " \n\t"} {
		if _, err := cli.SeedEdit(id, body, garden.BodySHA256(other), ""); err == nil {
			t.Fatalf("accepted an empty edit %q", body)
		}
	}
	if _, err := cli.SeedEdit(id, "missing base", "", ""); err == nil {
		t.Fatal("accepted an edit without a body hash")
	}
	if got := lifeShow(t, cli, id).Seed.Body; got != other {
		t.Fatalf("a refused edit lost the other writer's change: %q", got)
	}
	secondBody := strings.Replace(other, "Keep trailing", "Preserve trailing", 1)
	second, err := cli.SeedEdit(id, secondBody, garden.BodySHA256(other), "wording")
	if err != nil || second.BodyVersion != 3 {
		t.Fatalf("second edit: %+v, %v", second, err)
	}
	w.restart()
	cli = w.Client()
	for _, step := range []struct {
		target                *int
		wantVersion, restored int
		body                  string
	}{
		{nil, 4, 2, other},
		{nil, 5, 3, secondBody},
		{new(1), 6, 1, original},
	} {
		got, err := cli.SeedRollback(id, step.target)
		if err != nil || got.BodyVersion != step.wantVersion || got.RestoredVersion != step.restored || got.Seed.Body != step.body {
			t.Fatalf("rollback: %+v, %v; want v%d restoring v%d (%q)", got, err, step.wantVersion, step.restored, step.body)
		}
	}
	if _, err := cli.SeedRollback(id, new(99)); err == nil {
		t.Fatal("accepted a nonexistent body version")
	}
	notes, err := cli.SeedNotes("", id, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, note := range notes.Notes {
		if note.Kind != garden.NoteKindEdit {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(note.Body, "body v%d:", &version); err != nil {
			t.Fatal(err)
		}
		if seen[version] {
			t.Fatalf("duplicate edit entry for v%d", version)
		}
		seen[version] = true
	}
	if len(seen) != 5 || !seen[2] || !seen[6] {
		t.Fatalf("edit history has gaps or extra writes: %+v", seen)
	}
	encoded, err := json.Marshal(notes)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Keep trailing whitespace", "Preserve trailing whitespace", "Another writer's change"} {
		if strings.Contains(string(encoded), text) {
			t.Fatalf("notes JSON carries body text: %s", encoded)
		}
	}
	if !strings.Contains(string(encoded), "add coordination") || !strings.Contains(string(encoded), "wording") {
		t.Fatalf("edit notes missing from JSON: %s", encoded)
	}
}

func TestASeedCanStartEmptyButEditsAndRollbackCannotClearIt(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	planted, err := cli.SeedPlant("", "Start with a title", "", "", "")
	if err != nil || planted.Seed.Body != "" {
		t.Fatalf("bodyless planting: %+v, %v", planted, err)
	}
	id := planted.Seed.ID
	edited, err := cli.SeedEdit(id, "The agreed task.", garden.BodySHA256(""), "fill in the task")
	if err != nil || edited.BodyVersion != 2 {
		t.Fatalf("first edit: %+v, %v", edited, err)
	}
	if _, err := cli.SeedRollback(id, nil); err == nil || !strings.Contains(err.Error(), "nonempty") {
		t.Fatalf("rollback to an empty body: %v", err)
	}
	if shown := lifeShow(t, cli, id).Seed; shown.Body != edited.Seed.Body || shown.Rev != edited.Seed.Rev {
		t.Fatalf("refused rollback changed the seed: %+v", shown)
	}
	notes, err := cli.SeedNotes("", id, client.SeedNotesQuery{})
	if err != nil || notes.Total != 1 {
		t.Fatalf("refused rollback changed the log: %+v, %v", notes, err)
	}
}

func TestCrewBodyEditsAndRollbacksNameTheirMember(t *testing.T) {
	w := newCrewWorld(t)
	if err := w.InjectCrewSession("editor", "Editor", w.Path("editor"), "keel"); err != nil {
		t.Fatal(err)
	}
	cli := w.Client().WithGardenProfile("", "editor")
	planted, err := cli.SeedPlant("editor", "Member edits", "First body.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := planted.Seed.ID
	if _, err := cli.SeedEdit(id, "Second body.", garden.BodySHA256(planted.Seed.Body), "revise"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedRollback(id, nil); err != nil {
		t.Fatal(err)
	}
	notes, err := cli.SeedNotes("editor", id, client.SeedNotesQuery{})
	if err != nil || notes.Total != 2 {
		t.Fatalf("edit log: %+v, %v", notes, err)
	}
	for _, note := range notes.Notes {
		if note.Kind != garden.NoteKindEdit || authorMemberOf(note) != "keel" {
			t.Fatalf("member edit attribution: %+v", note)
		}
	}
}

func TestBodiesWrittenByAnOlderBuildSurviveTheReturnToThisBuild(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	planted := func(title, body string) string {
		t.Helper()
		got, err := cli.SeedPlant("", title, body, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return got.Seed.ID
	}
	const (
		split      = "Text the older build could not see"
		oldEdit    = "Replacement typed on the older build chrysoberyl"
		oldPlant   = "Planted on the older build tourmaline"
		legacySeed = "s-7k3f9a"
	)
	edited := planted("Edited on the older build", split)
	parked := planted("Parked on the older build", split)
	same := planted("Same text in both places", split)
	empty := planted("Filled on the older build", "")

	w.stop()
	db, err := sql.Open("sqlite3", filepath.Join(w.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	var collection int64
	if err := db.QueryRow(`SELECT id FROM document_collections WHERE namespace = ? AND collection = ?`, garden.Namespace, garden.CollectionSeeds).Scan(&collection); err != nil {
		t.Fatal(err)
	}
	table := docstore.TableName(collection)
	setInline := func(id, body string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE `+table+` SET body = json_set(body, '$.body', ?), rev = rev + 1 WHERE id = ?`, body, id); err != nil {
			t.Fatal(err)
		}
	}
	setInline(edited, oldEdit)
	setInline(parked, "")
	setInline(same, split)
	setInline(empty, oldEdit)
	if _, err := db.Exec(`INSERT INTO `+table+` (id, body, rev, created_at, updated_at)
		SELECT ?, json_set(body, '$.id', ?, '$.title', 'Beryl garnet', '$.body', ?), 1, created_at, updated_at FROM `+table+` WHERE id = ?`,
		legacySeed, legacySeed, oldPlant, parked); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	w.start()
	cli = w.Client()
	body := func(id string) string { t.Helper(); return lifeShow(t, cli, id).Seed.Body }
	for id, want := range map[string]string{edited: oldEdit, parked: split, same: split, empty: oldEdit, legacySeed: oldPlant} {
		if got := body(id); got != want {
			t.Fatalf("%s after returning: %q, want %q", id, got, want)
		}
		lifeMove(t, cli, "", id, "park", "", "")
		if got := body(id); got != want {
			t.Fatalf("%s after parking: %q, want %q", id, got, want)
		}
	}
	for _, id := range []string{parked, same, empty, legacySeed} {
		if _, err := cli.SeedRollback(id, nil); err == nil {
			t.Fatalf("%s had no competing text but gained a body version", id)
		}
	}
	notes, err := cli.SeedNotes("", edited, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(notes.Notes); !strings.Contains(string(encoded), "older attn build") {
		t.Fatalf("the log does not say which text replaced which: %s", encoded)
	}
	for word, want := range map[string]string{"tourmaline": legacySeed, "garnet": legacySeed, "chrysoberyl": edited} {
		found, err := cli.SeedSearch("", word, 10)
		if err != nil {
			t.Fatal(err)
		}
		if encoded, _ := json.Marshal(found); !strings.Contains(string(encoded), want) {
			t.Fatalf("search for %q missed %s: %s", word, want, encoded)
		}
	}
	w.restart()
	cli = w.Client()
	if got := body(legacySeed); got != oldPlant {
		t.Fatalf("after a second restart: %q", got)
	}
	restored, err := cli.SeedRollback(edited, nil)
	if err != nil || restored.Seed.Body != split {
		t.Fatalf("rolling back to the text the older build could not see: %+v, %v", restored, err)
	}
}
