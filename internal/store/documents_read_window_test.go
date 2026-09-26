package store

import (
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func readIDs(t *testing.T, s *Store, q docstore.Query) ([]string, error) {
	t.Helper()
	read, found, err := s.ReadQuery(q)
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatalf("%s/%s is not declared", q.Namespace, q.Collection)
	}
	ids := make([]string, 0, len(read.Documents))
	for _, d := range read.Documents {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func pagedByAttempts(after string) docstore.Query {
	return docstore.Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Sort:       &docstore.Sort{Field: "attempts"},
		After:      after,
	}
}

func TestAQueryNeverSeesACollectionThatNeverExisted(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{}`,
		"b": `{"attempts":3}`,
		"c": `{"attempts":7}`,
	})
	schema := requestsDecl(t, s)

	const reads = 3000
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		bodies := []string{`{}`, `{"attempts":5}`}
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if _, err := s.PutDocument(schema, "a", []byte(bodies[i%2]), base.Add(time.Duration(i)*time.Millisecond), nil); err != nil {
				t.Errorf("writer: %v", err)
				return
			}
		}
	}()

	legal := [][]string{{"b", "c"}, {"c"}}
	for i := 0; i < reads; i++ {
		got, err := readIDs(t, s, pagedByAttempts("a"))
		if err != nil {
			close(done)
			wg.Wait()
			t.Fatalf("read %d: %v", i, err)
		}
		ok := false
		for _, want := range legal {
			if sameIDs(got, want) {
				ok = true
				break
			}
		}
		if !ok {
			close(done)
			wg.Wait()
			t.Fatalf("read %d answered %v, which is the page after \"a\" in no state this collection was ever in; legal answers are %v",
				i, got, legal)
		}
	}
	close(done)
	wg.Wait()
}
