package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func factsOf(t *testing.T, d *Daemon) []store.BusEvent {
	t.Helper()
	events, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

func docFacts(t *testing.T, d *Daemon, name string) []store.BusEvent {
	t.Helper()
	var out []store.BusEvent
	for _, e := range factsOf(t, d) {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func TestAWriteAppendsTheDocumentChangedFact(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"pending"}`,
		})
	})
	if !resp.Ok {
		t.Fatalf("put: %v", protocol.Deref(resp.Error))
	}

	facts := docFacts(t, d, "document.changed")
	if len(facts) != 1 {
		t.Fatalf("one write appended %d fact(s)", len(facts))
	}
	fact := facts[0]
	if fact.Subject != testDocNS+"/"+testDocColl+"/a" {
		t.Fatalf("subject = %q, want the document's address", fact.Subject)
	}

	var payload struct {
		Namespace  string `json:"namespace"`
		Collection string `json:"collection"`
		ID         string `json:"id"`
		Deleted    bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(fact.Payload), &payload); err != nil {
		t.Fatalf("payload %q does not decode: %v", fact.Payload, err)
	}
	if payload.Namespace != testDocNS || payload.Collection != testDocColl || payload.ID != "a" {
		t.Fatalf("payload names %+v", payload)
	}
	if payload.Deleted {
		t.Fatal("a write was announced as a removal")
	}

	if resp.DocPutResult.Seq != int(fact.Seq) {
		t.Fatalf("the put reported seq %d, the fact is at %d", resp.DocPutResult.Seq, fact.Seq)
	}
}

func TestARemovalIsAnnouncedAsADeletion(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	if !deleteDoc(t, d, "a") {
		t.Fatal("delete reported the document was not there")
	}

	facts := docFacts(t, d, "document.changed")
	if len(facts) != 2 {
		t.Fatalf("a write and a removal appended %d fact(s), want 2", len(facts))
	}
	var payload struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(facts[1].Payload), &payload); err != nil {
		t.Fatalf("payload does not decode: %v", err)
	}
	if payload.ID != "a" || !payload.Deleted {
		t.Fatalf("removal announced as %+v", payload)
	}
}

func TestAWriteThatChangedNothingAppendsNoFact(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	before := len(docFacts(t, d, "document.changed"))

	if deleteDoc(t, d, "gone-already") {
		t.Fatal("deleting a document that was never there reported it existed")
	}

	stale := 99
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl,
			ID: "a", Body: `{"status":"approved"}`, ExpectedRev: &stale,
		})
	})
	if resp.Ok {
		t.Fatal("a stale conditional write was accepted")
	}

	if after := len(docFacts(t, d, "document.changed")); after != before {
		t.Fatalf("writes that changed nothing appended %d fact(s)", after-before)
	}
}

func TestUndefiningACollectionAnnouncesTheCollection(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"pending"}`)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocUndefine(c, &protocol.DocUndefineMessage{
			Cmd: protocol.CmdDocUndefine, Namespace: testDocNS, Collection: testDocColl,
		})
	})
	if !resp.Ok {
		t.Fatalf("undefine: %v", protocol.Deref(resp.Error))
	}

	removals := docFacts(t, d, "document.collection.removed")
	if len(removals) != 1 {
		t.Fatalf("undefine appended %d removal fact(s), want 1", len(removals))
	}
	if removals[0].Subject != testDocNS+"/"+testDocColl {
		t.Fatalf("subject = %q, want the collection's address", removals[0].Subject)
	}
	var payload struct {
		Namespace  string `json:"namespace"`
		Collection string `json:"collection"`
		Documents  int    `json:"documents"`
	}
	if err := json.Unmarshal([]byte(removals[0].Payload), &payload); err != nil {
		t.Fatalf("payload %q does not decode: %v", removals[0].Payload, err)
	}
	if payload.Namespace != testDocNS || payload.Collection != testDocColl || payload.Documents != 2 {
		t.Fatalf("removal payload = %+v", payload)
	}

	for _, fact := range docFacts(t, d, "document.changed") {
		if fact.Subject == testDocNS+"/"+testDocColl+"/" {
			t.Fatalf("undefine announced a document with no id: %+v", fact)
		}
	}
}
