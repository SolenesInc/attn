package store

import (
	"strings"
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

func TestPagingAfterADeletedAnchorSaysSoRatherThanEmptyingThePage(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	if _, err := s.DeleteDocument(requestsDecl(t, s), "a", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := readIDs(t, s, pagedByAttempts("a"))
	if err == nil {
		t.Fatalf("paging after a deleted anchor returned %v and no error; it must say the anchor is gone", got)
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("error does not name the missing anchor: %v", err)
	}
}

func TestAPageNeverContainsItsOwnAnchor(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{}`,
		"b": `{"attempts":3}`,
		"c": `{"attempts":7}`,
	})

	got, err := readIDs(t, s, pagedByAttempts("a"))
	if err != nil {
		t.Fatalf("page after a null-valued anchor: %v", err)
	}
	if want := []string{"b", "c"}; !sameIDs(got, want) {
		t.Fatalf("page after a null-valued anchor is %v, want %v", got, want)
	}

	if _, err := s.PutDocument(requestsDecl(t, s), "a", []byte(`{"attempts":5}`), base.Add(time.Hour), nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err = readIDs(t, s, pagedByAttempts("a"))
	if err != nil {
		t.Fatalf("page after a valued anchor: %v", err)
	}
	if want := []string{"c"}; !sameIDs(got, want) {
		t.Fatalf("page after a valued anchor is %v, want %v", got, want)
	}
}

func TestAFilterIsBoundUnderTheDeclarationItRunsAgainst(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	numeric := docstore.Query{
		Namespace:  "app/approval-gate",
		Collection: "requests",
		Filters:    []docstore.Filter{{Field: "attempts", Op: docstore.OpEq, Value: float64(2)}},
	}

	got, err := readIDs(t, s, numeric)
	if err != nil {
		t.Fatalf("numeric filter under a number declaration: %v", err)
	}
	if want := []string{"b"}; !sameIDs(got, want) {
		t.Fatalf("numeric filter matched %v, want %v", got, want)
	}

	next := requestsDeclaration()
	for i := range next.Fields {
		if next.Fields[i].Name == "attempts" {
			next.Fields[i].Type = docstore.FieldString
		}
	}
	if _, err := s.DefineDocumentCollection(next, base.Add(time.Hour)); err != nil {
		t.Fatalf("redeclare: %v", err)
	}

	got, err = readIDs(t, s, numeric)
	if err == nil {
		t.Fatalf("a numeric filter on a string field returned %v and no error", got)
	}
	if !strings.Contains(err.Error(), "needs a string value") {
		t.Fatalf("error does not name the type mismatch: %v", err)
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
