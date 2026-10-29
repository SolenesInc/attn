package daemon_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
)

func TestSeedBodyEditsRefuseLostWritesAndKeepRollbackHistory(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	const original = "# Work\n\nKeep trailing whitespace.  \n\n"
	planted, err := cli.SeedPlant("", "Body history", original, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := planted.Seed.ID
	if _, err := cli.SeedRollback(id, nil); err == nil {
		t.Fatal("a seed with no edits offered a rollback")
	}
	base := garden.BodySHA256(original)
	if _, err := cli.SeedNote("", id, "A note changes the revision, not the body.", "", "", false, nil); err != nil {
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
	notes, err := cli.SeedNotes("", id, 0)
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
