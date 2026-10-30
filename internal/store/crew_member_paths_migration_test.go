package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
)

func TestCrewPathsUpgradePreservesMembersWithoutMachinePaths(t *testing.T) {
	for _, letter := range []string{"/Users/alex/.attn/crew/keel/handoffs/letter.md", "/home/alex/.attn/crew/keel/handoffs/letter.md", "handoffs/letter.md", ""} {
		t.Run(letter, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			s, err := newStoreAtVersion(path, 1792096233604385)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			schema := crew.MembersSchema()
			if _, err := defineLegacyDocumentCollection(s, schema, time.Now()); err != nil {
				t.Fatal(err)
			}
			declared, _, err := legacyDocumentCollection(s, schema.Namespace, schema.Collection)
			if err != nil {
				t.Fatal(err)
			}
			schema = *declared
			before := map[string]any{
				"id": "keel", "home_dir": "/Users/alex/.attn/crew/keel", "charter_path": "/Users/alex/.attn/crew/keel/CHARTER.md",
				"letter_path": letter, "letter_session": "previous", "binding_session": "current", "cwd": "/work/project",
				"awareness_dirs": []any{"/work/notes"}, "agent": "codex", "model": "sol", "effort": "high", "autonomous_wakes": []any{"2026-10-15T12:00:00Z"},
			}
			body, err := json.Marshal(before)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutDocument(schema, "keel", body, time.Now(), nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = NewWithDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			doc, found, err := s.GetDocument(schema, "keel")
			if err != nil || !found {
				t.Fatalf("upgraded member missing: %v", err)
			}
			var after map[string]any
			if err := json.Unmarshal(doc.Body, &after); err != nil {
				t.Fatal(err)
			}
			delete(before, "home_dir")
			delete(before, "charter_path")
			delete(before, "letter_path")
			if letter != "" {
				before["letter_file"] = "letter.md"
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("upgraded record = %s, want %v", doc.Body, before)
			}
		})
	}
}
