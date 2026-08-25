package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDocumentsOverARealSocket(t *testing.T) {
	useFreeWSPort(t)
	sockPath := filepath.Join(shortTempDir(t), "attn.sock")

	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)

	c := client.New(sockPath)

	if _, err := c.DocDefine(protocol.DocumentCollectionSchema{
		Namespace:  testDocNS,
		Collection: testDocColl,
		Fields:     []protocol.DocumentFieldSpec{{Name: "status", Type: "string"}},
	}); err != nil {
		t.Fatalf("define: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := c.DocPut(testDocNS, testDocColl, id, `{"status":"pending"}`, nil); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}

	q := protocol.DocumentQuery{Namespace: testDocNS, Collection: testDocColl}
	read, err := c.DocQuery(q)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(read.Documents) != 2 {
		t.Fatalf("query returned %d documents, want 2", len(read.Documents))
	}
	if read.AsOfSeq <= 0 {
		t.Fatalf("query answered as of seq %d; two writes have landed", read.AsOfSeq)
	}

	// Written from inside the callback so it lands while the read loop is running.
	var windows []client.DocWindow
	// The seq travels over a channel rather than a variable the loop would race.
	putSeq := make(chan int, 1)
	err = c.DocSubscribe(q, nil, func(w client.DocWindow) bool {
		windows = append(windows, w)
		if len(windows) == 1 {
			go func() {
				result, err := c.DocPut(testDocNS, testDocColl, "c", `{"status":"pending"}`, nil)
				if err != nil {
					t.Errorf("live put: %v", err)
					putSeq <- 0
					return
				}
				putSeq <- result.Seq
			}()
		}
		return len(windows) < 2
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := len(windows[0].Changed); got != 2 {
		t.Fatalf("the first window sent %d bodies, want 2 — a fresh subscriber holds nothing", got)
	}
	live := windows[1]
	if len(live.Documents) != 3 {
		t.Fatalf("the live window holds %d documents, want 3", len(live.Documents))
	}
	if len(live.Changed) != 1 || live.Changed[0] != "c" {
		t.Fatalf("the live window sent %v, want only the document that was written", live.Changed)
	}
	if seq := <-putSeq; live.AsOfSeq < int64(seq) {
		t.Fatalf("the live window is as of seq %d, below the write it reflects at %d", live.AsOfSeq, seq)
	}

	held := live.Documents
	if _, err := c.DocDelete(testDocNS, testDocColl, "a", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.DocPut(testDocNS, testDocColl, "b", `{"status":"approved"}`, nil); err != nil {
		t.Fatalf("edit: %v", err)
	}

	var resumed client.DocWindow
	err = c.DocSubscribe(q, held, func(w client.DocWindow) bool {
		resumed = w
		return false
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(resumed.Documents) != 2 {
		t.Fatalf("resumed window holds %d documents, want 2", len(resumed.Documents))
	}
	if len(resumed.Changed) != 1 || resumed.Changed[0] != "b" {
		t.Fatalf("resume sent %v, want only the document edited while away", resumed.Changed)
	}
	for _, doc := range resumed.Documents {
		switch doc.ID {
		case "b":
			if doc.Body != `{"status":"approved"}` {
				t.Fatalf("b applied as %s", doc.Body)
			}
		case "c":
			if doc.Body != `{"status":"pending"}` {
				t.Fatalf("c applied as %s", doc.Body)
			}
		default:
			t.Fatalf("resumed window holds %q", doc.ID)
		}
	}

	ended := make(chan error, 1)
	opened := make(chan struct{})
	go func() {
		first := true
		ended <- c.DocSubscribe(q, nil, func(client.DocWindow) bool {
			if first {
				first = false
				close(opened)
			}
			return true
		})
	}()
	<-opened
	if _, err := c.DocUndefine(testDocNS, testDocColl); err != nil {
		t.Fatalf("undefine: %v", err)
	}
	err = <-ended
	code, isEnd := client.DocSubscriptionCode(err)
	if !isEnd {
		t.Fatalf("the subscription ended with %v, which is not a subscription ending", err)
	}
	if code != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("subscription ended with code %q, want %q", code, protocol.ErrorCodeCollectionUndefined)
	}
}
