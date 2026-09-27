package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppHearsEachDocumentChangeAndNothingForWritesThatChangedNothing(t *testing.T) {
	runtime := testworld.NewFakeAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	declaration := appManifestDeclaration(t, appbuild.Manifest{Name: "doc-watcher", Subscribe: []appbuild.Subscribe{{Events: []string{"document.changed", "document.collection.removed"}}}})
	applyAppVersion(t, cli, "doc-watcher", declaration, "export default {}\n")
	if _, err := cli.AppRuntimeRestart(); err != nil {
		t.Fatal(err)
	}
	handler := runtime.Connect(w.DialUnix)

	defineRequests(t, cli, gateNS)
	written := put(t, cli, gateNS, "a", `{"status":"pending"}`)
	put(t, cli, gateNS, "b", `{"status":"pending"}`)
	if missing, err := cli.DocDelete(gateNS, requests, "never-existed", nil); err != nil || missing.Existed {
		t.Fatalf("deleting a missing document = %+v, %v", missing, err)
	}
	stale := 99
	if _, err := cli.DocPut(gateNS, requests, "a", `{"status":"approved"}`, &stale); client.ErrorCode(err) != protocol.ErrorCodeConflict {
		t.Fatalf("a stale write returned %v, want a conflict", err)
	}
	if _, err := cli.DocDelete(gateNS, requests, "a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.DocUndefine(gateNS, requests); err != nil {
		t.Fatal(err)
	}

	for i, want := range []struct{ name, subject, payload string }{
		{"document.changed", gateNS + "/requests/a", `{"namespace":"app/approval-gate","collection":"requests","id":"a"}`},
		{"document.changed", gateNS + "/requests/b", `{"namespace":"app/approval-gate","collection":"requests","id":"b"}`},
		{"document.changed", gateNS + "/requests/a", `{"namespace":"app/approval-gate","collection":"requests","id":"a","deleted":true}`},
		{"document.collection.removed", gateNS + "/requests", `{"namespace":"app/approval-gate","collection":"requests","documents":1}`},
	} {
		got := handler.NextDispatch()
		if got.App != "doc-watcher" || got.Name != want.name || got.Subject != want.subject || string(got.Payload) != want.payload {
			t.Fatalf("event %d the app heard = %s %s %s, want %s %s %s", i+1, got.Name, got.Subject, got.Payload, want.name, want.subject, want.payload)
		}
		if i == 0 && got.Seq != int64(written.Seq) {
			t.Errorf("the first write reported seq %d, but the app heard it at %d", written.Seq, got.Seq)
		}
	}
}
