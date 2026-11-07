package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRememberDocumentsNeverReturnsAValueOlderThanAWriteCommittedDuringItsRead(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), time.Now()); err != nil {
		t.Fatal(err)
	}
	defined := declOf(t, s, "test/approval-gate", "requests")
	write := func(id string) {
		t.Helper()
		if _, err := s.PutDocument(defined, id, []byte(`{}`), time.Now(), nil); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	write("before")

	var memo DocumentsMemo[[]string]
	reads := 0
	read := func() ([]string, error) {
		reads++
		seen := []string{"before"}
		write("during")
		return seen, nil
	}
	refresh := func(previous []string, written []string) ([]string, error) {
		return append(slices.Clone(previous), written...), nil
	}

	got, err := RememberDocuments(s, &memo, defined, read, refresh)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "during") {
		t.Errorf("RememberDocuments returned %v, want the write committed during its read", got)
	}
	if reads != 1 {
		t.Errorf("RememberDocuments read the collection %d times, want the write caught up by a refresh", reads)
	}

	write("after")
	got, err = RememberDocuments(s, &memo, defined, read, refresh)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "after") || reads != 1 {
		t.Errorf("after a later write RememberDocuments returned %v with %d reads, want it refreshed", got, reads)
	}
}
