package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func refuseDraftClears(t *testing.T, d *Daemon) {
	t.Helper()
	d.stopEventBus()
	if err := d.store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "attn.db")
	persistent, err := store.NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	d.store = persistent
	d.eventBus = nil
	d.ensureEventBus()
	t.Cleanup(d.stopEventBus)
	direct, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = direct.Close() })
	if _, err := direct.Exec(`CREATE TRIGGER refuse_draft_clear BEFORE UPDATE ON markdown_annotation_drafts
		WHEN NEW.annotations_json = '[]' BEGIN SELECT RAISE(ABORT, 'disk refused the clear'); END`); err != nil {
		t.Fatal(err)
	}
}

func saveSubmitDraft(t *testing.T, d *Daemon, key string) {
	t.Helper()
	blob, err := json.Marshal([]protocol.MarkdownAnnotation{{ID: "g1", Type: "global", Text: protocol.Ptr("hi"), CreatedAt: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.SaveMarkdownAnnotationDraft(key, string(blob), 2, time.Now()); err != nil {
		t.Fatalf("save draft: %v", err)
	}
}

func sendSubmit(t *testing.T, d *Daemon, msg protocol.MarkdownAnnotationsSubmitMessage) protocol.MarkdownAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	msg.Cmd, msg.RequestID = protocol.CmdMarkdownAnnotationsSubmit, "req-1"
	d.handleMarkdownAnnotationsSubmit(client, &msg)
	var res protocol.MarkdownAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func requireSucceededWithUnclearedDraft(t *testing.T, res protocol.MarkdownAnnotationsSubmitResultMessage, status string) {
	t.Helper()
	if !res.Success || res.Status != status {
		t.Fatalf("result = %+v, want %s despite the clear failure", res, status)
	}
	if res.Error == nil || !strings.Contains(*res.Error, status+"; failed to clear drafts") {
		t.Fatalf("error = %v, want the %s-but-not-cleared warning", res.Error, status)
	}
	if res.Generation != nil {
		t.Fatalf("generation = %d, want none when the clear failed", *res.Generation)
	}
}

func TestMarkdownAnnotationsSubmitClearFailureStillDelivered(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	refuseDraftClears(t, d)
	typed := 0
	d.ptyBackend = &fakeSpawnBackend{onInput: func(string, []byte) { typed++ }}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: "target", Label: "target", Agent: protocol.SessionAgentClaude, Directory: "/tmp/target",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now})
	path := "/tmp/annotated-doc.md"
	saveSubmitDraft(t, d, path)

	res := sendSubmit(t, d, protocol.MarkdownAnnotationsSubmitMessage{
		DocumentUri: fileDocumentURI(path), SourceKind: annotationSourceFile,
		Path: protocol.Ptr(path), TargetSessionID: protocol.Ptr("target"),
	})

	requireSucceededWithUnclearedDraft(t, res, annotationSubmitStatusDelivered)
	if typed == 0 {
		t.Fatal("the annotations were never typed into the session")
	}
}

func TestMarkdownAnnotationsSubmitNoteClearFailureStillReportsNoted(t *testing.T) {
	d := newGardenDaemon(t)
	refuseDraftClears(t, d)
	d.ensureGardenCollections()
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Keep one note"})
	saveSubmitDraft(t, d, seedDocumentURI(seed.ID))

	res := sendSubmit(t, d, protocol.MarkdownAnnotationsSubmitMessage{
		DocumentUri: seedDocumentURI(seed.ID), SourceKind: annotationSourceSeed,
		SeedID: protocol.Ptr(seed.ID), TargetSeedID: protocol.Ptr(seed.ID),
	})

	requireSucceededWithUnclearedDraft(t, res, annotationSubmitStatusNoted)
}
