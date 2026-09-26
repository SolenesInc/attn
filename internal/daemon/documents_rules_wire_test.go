package daemon_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func docRefusal(err error) (code, message string) {
	var refused *client.DaemonError
	if errors.As(err, &refused) {
		return refused.Code, refused.Message
	}
	if err != nil {
		return "", err.Error()
	}
	return "", ""
}

func TestBadDocumentRequestsAreRefusedUpFrontWithWhatToDo(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)
	queried := func(q protocol.DocumentQuery) func(*testing.T) (string, string) {
		return func(*testing.T) (string, string) {
			_, err := cli.DocQuery(q)
			return docRefusal(err)
		}
	}
	subscribed := func(q protocol.DocumentQuery) func(*testing.T) (string, string) {
		return func(t *testing.T) (string, string) {
			resp := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: q}).next(t)
			if resp.Ok {
				return "", ""
			}
			return protocol.Deref(resp.ErrorCode), protocol.Deref(resp.Error)
		}
	}
	sortedBy := func(field string) protocol.DocumentQuery {
		q := requestsWhere(gateNS)
		q.Sort = &protocol.DocumentSort{Field: field}
		return q
	}
	after := func(id string) protocol.DocumentQuery {
		q := requestsWhere(gateNS)
		q.After = protocol.Ptr(id)
		return q
	}

	for _, c := range []struct {
		name     string
		ask      func(*testing.T) (string, string)
		wantCode string
		mentions []string
	}{
		{"a read of an undeclared collection", queried(protocol.DocumentQuery{Namespace: gateNS, Collection: "nope"}),
			protocol.ErrorCodeUndeclaredCollection, []string{gateNS, "nope", "doc define"}},
		{"a filter on an undeclared field", queried(requestsWhere(gateNS, where("not-declared", "eq", "x"))),
			protocol.ErrorCodeInvalidQuery, []string{"not-declared"}},
		{"a filter bound of the wrong type", queried(requestsWhere(gateNS, where("status", "eq", 5))),
			protocol.ErrorCodeInvalidQuery, []string{"status"}},
		{"an after cursor to a document that is gone", queried(after("gone")),
			"", []string{"gone", "no longer exists"}},
		{"a live query sorted by an undeclared field", subscribed(sortedBy("undeclared")),
			protocol.ErrorCodeInvalidQuery, []string{"undeclared"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, message := c.ask(t)
			if message == "" {
				t.Fatal("the request was accepted")
			}
			if c.wantCode != "" && code != c.wantCode {
				t.Errorf("refused with code %q (%s), want %q", code, message, c.wantCode)
			}
			for _, want := range c.mentions {
				if !strings.Contains(message, want) {
					t.Errorf("the refusal %q does not mention %q", message, want)
				}
			}
		})
	}
}

func TestRemovingOrRedeclaringACollectionEndsTheLiveQueriesThatNeedIt(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)

	everything := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: requestsWhere(gateNS)})
	everything.window(t)
	removed, err := cli.DocUndefine(gateNS, requests)
	if err != nil || removed.DocumentsRemoved != 1 {
		t.Fatalf("undefine = %+v, %v; want the one document removed", removed, err)
	}
	if ended := everything.next(t); ended.Ok || protocol.Deref(ended.ErrorCode) != protocol.ErrorCodeCollectionUndefined ||
		!strings.Contains(protocol.Deref(ended.Error), "is not declared") {
		t.Fatalf("after the collection went its live query got %+v; want collection_undefined saying it is not declared", ended)
	}

	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)
	pending := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: requestsWhere(gateNS, where("status", "eq", "pending"))})
	if opened := pending.window(t); strings.Join(opened.Order, " ") != "a" {
		t.Fatalf("the filtered live query opened on %v, want a", opened.Order)
	}
	defineCollection(t, cli, gateNS, requests)
	if ended := pending.next(t); ended.Ok || protocol.Deref(ended.ErrorCode) != protocol.ErrorCodeCollectionRedeclared ||
		!strings.Contains(protocol.Deref(ended.Error), "status") {
		t.Fatalf("after status was dropped from the declaration the live query got %+v; want collection_redeclared naming status", ended)
	}
}

func TestConditionalDocumentWritesRefuseStaleRevisionsAndChangeNothing(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)

	if written := put(t, cli, gateNS, "a", `{"status":"pending"}`); written.Rev != 1 || get(t, cli, gateNS, "a").Document.Rev != 1 {
		t.Fatalf("the first write reported rev %d and reads back at rev %d, want 1 for both", written.Rev, get(t, cli, gateNS, "a").Document.Rev)
	}
	put(t, cli, gateNS, "a", `{"status":"approved"}`)

	_, err := cli.DocPut(gateNS, requests, "a", `{"status":"rejected"}`, protocol.Ptr(1))
	var refused *client.DaemonError
	if !errors.As(err, &refused) || refused.Code != protocol.ErrorCodeConflict ||
		!strings.Contains(refused.Message, "rev 1") || !strings.Contains(refused.Message, "rev 2") {
		t.Fatalf("a write expecting a replaced revision = %v; want a conflict naming both revisions", err)
	}
	if c := refused.Conflict; c == nil || c.Namespace != gateNS || c.Collection != requests || c.ID != "a" || c.Expected != 1 || c.Actual != 2 || !c.Found {
		t.Fatalf("the conflict names %+v; want a in %s/%s expected at rev 1, found at rev 2", c, gateNS, requests)
	}
	if doc := get(t, cli, gateNS, "a").Document; doc.Body != `{"status":"approved"}` || doc.Rev != 2 {
		t.Fatalf("the refused write left a as %s at rev %d", doc.Body, doc.Rev)
	}

	if _, err := cli.DocPut(gateNS, requests, "b", `{"status":"first"}`, protocol.Ptr(0)); err != nil {
		t.Fatalf("a create-only write of a new document: %v", err)
	}
	if _, err := cli.DocPut(gateNS, requests, "b", `{"status":"second"}`, protocol.Ptr(0)); client.ErrorCode(err) != protocol.ErrorCodeConflict {
		t.Fatalf("a second create-only write = %v, want a conflict", err)
	}
	if body := get(t, cli, gateNS, "b").Document.Body; body != `{"status":"first"}` {
		t.Fatalf("the losing create left b as %s", body)
	}

	if _, err := cli.DocDelete(gateNS, requests, "a", protocol.Ptr(1)); client.ErrorCode(err) != protocol.ErrorCodeConflict {
		t.Fatalf("a delete expecting a replaced revision = %v, want a conflict", err)
	}
	if !get(t, cli, gateNS, "a").Found {
		t.Fatal("the refused delete removed the document")
	}
}

func TestADocumentCountHonoursFiltersButNotTheLimit(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)
	put(t, cli, gateNS, "b", `{"status":"pending"}`)
	put(t, cli, gateNS, "c", `{"status":"approved"}`)
	limited := requestsWhere(gateNS)
	limited.Limit = protocol.Ptr(1)

	for _, c := range []struct {
		name string
		q    protocol.DocumentQuery
		want int
	}{
		{"everything", requestsWhere(gateNS), 3},
		{"a filter", requestsWhere(gateNS, where("status", "eq", "pending")), 2},
		{"a limit of one", limited, 3},
	} {
		if counted, err := cli.DocCount(c.q); err != nil || counted.Count != c.want || counted.AsOfSeq <= 0 {
			t.Errorf("counting %s = %+v, %v; want %d at a position on the log", c.name, counted, err, c.want)
		}
	}
}

func TestTheGenericDocumentCommandsCannotChangeTheGarden(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"define", func() error {
			_, err := cli.DocDefine(protocol.DocumentCollectionSchema{Namespace: "core/garden", Collection: "raw"})
			return err
		}},
		{"undefine", func() error { _, err := cli.DocUndefine("core/garden", "seeds"); return err }},
		{"put", func() error { _, err := cli.DocPut("core/garden", "seeds", "s-7k3f9m", `{}`, nil); return err }},
		{"delete", func() error { _, err := cli.DocDelete("core/garden", "seeds", "s-7k3f9m", nil); return err }},
	} {
		if err := c.call(); err == nil || !strings.Contains(err.Error(), "use attn seed commands") {
			t.Errorf("a generic %s in the Garden = %v, want a refusal pointing at attn seed commands", c.name, err)
		}
	}
}
