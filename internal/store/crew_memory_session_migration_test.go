package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func TestCrewMemorySessionMigrationPreservesPendingLetters(t *testing.T) {
	for _, body := range []string{
		`{"binding_session":"current","letter_session":"current","letter_file":"letter.md","letter_day":"Keep the reasons.","letter_close":"sleep"}`,
		`{"letter_day":""}`,
		`{"letter_file":"letter.md"}`,
	} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			s, err := newStoreAtVersion(path, 1793138901999842)
			if err != nil {
				t.Fatal(err)
			}
			schema := docstore.CollectionSchema{Namespace: "core/crew", Collection: "members"}
			if _, err := s.DefineDocumentCollection(schema, time.Now()); err != nil {
				t.Fatal(err)
			}
			stored, _, err := s.DocumentCollection(schema.Namespace, schema.Collection)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutDocument(*stored, "trellis", []byte(body), time.Now(), nil); err != nil {
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
			doc, found, err := s.GetDocument(*stored, "trellis")
			if err != nil || !found {
				t.Fatalf("migrated member: found=%t err=%v", found, err)
			}
			var want, got map[string]any
			if err := json.Unmarshal([]byte(body), &want); err != nil {
				t.Fatal(err)
			}
			if summary, ok := want["letter_day"]; ok {
				want["letter_summary"] = summary
				delete(want, "letter_day")
			}
			if err := json.Unmarshal(doc.Body, &got); err != nil {
				t.Fatal(err)
			}
			wantJSON, _ := json.Marshal(want)
			gotJSON, _ := json.Marshal(got)
			if string(wantJSON) != string(gotJSON) {
				t.Fatalf("migrated letter = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}
