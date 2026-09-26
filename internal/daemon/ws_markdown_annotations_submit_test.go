package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const submitTestPath = "/tmp/annotated-doc.md"

func newMarkdownAnnotationsDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func seedSubmitDraft(t *testing.T, d *Daemon, generation int, anns []protocol.MarkdownAnnotation) {
	t.Helper()
	blob, err := json.Marshal(anns)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.SaveMarkdownAnnotationDraft(submitTestPath, string(blob), generation, time.Now()); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
}

func submitTestAnnotations() []protocol.MarkdownAnnotation {
	return []protocol.MarkdownAnnotation{
		{ID: "c1", Type: "comment", Anchor: mdAnchor(3, 3, 0, "hello"), Text: protocol.Ptr("hi"), CreatedAt: 1},
	}
}

func sendSubmit(t *testing.T, d *Daemon, target string, orphaned []string) protocol.MarkdownAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsSubmit(client, &protocol.MarkdownAnnotationsSubmitMessage{
		Cmd:             protocol.CmdMarkdownAnnotationsSubmit,
		DocumentUri:     fileDocumentURI("workspace-test", submitTestPath),
		SourceKind:      annotationSourceFile,
		WorkspaceID:     protocol.Ptr("workspace-test"),
		Path:            protocol.Ptr(submitTestPath),
		TargetSessionID: protocol.Ptr(target),
		OrphanedIds:     orphaned,
		RequestID:       "req-1",
	})
	var res protocol.MarkdownAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventMarkdownAnnotationsSubmitResult || res.RequestID != "req-1" {
		t.Fatalf("unexpected result envelope: %+v", res)
	}
	return res
}

func seedSeedSubmitDraft(t *testing.T, d *Daemon, seedID string, generation int) {
	t.Helper()
	blob, err := json.Marshal(submitTestAnnotations())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.SaveMarkdownAnnotationDraft(seedDocumentURI(seedID), string(blob), generation, time.Now()); err != nil {
		t.Fatalf("seed seed draft: %v", err)
	}
}

func sendSeedSubmit(t *testing.T, d *Daemon, sourceSeed string, targetSession, targetSeed *string) protocol.MarkdownAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsSubmit(client, &protocol.MarkdownAnnotationsSubmitMessage{
		Cmd:             protocol.CmdMarkdownAnnotationsSubmit,
		DocumentUri:     seedDocumentURI(sourceSeed),
		SourceKind:      annotationSourceSeed,
		SeedID:          protocol.Ptr(sourceSeed),
		TargetSessionID: targetSession,
		TargetSeedID:    targetSeed,
		RequestID:       "req-seed",
	})
	var res protocol.MarkdownAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func storedSubmitDraftCount(t *testing.T, d *Daemon) int {
	t.Helper()
	draft, err := d.store.GetMarkdownAnnotationDraft(submitTestPath)
	if err != nil {
		t.Fatal(err)
	}
	anns, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		t.Fatal(err)
	}
	return len(anns)
}

func TestMarkdownAnnotationsSubmitNoteClearFailureStillReportsNoted(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Keep one note"})
	seedSeedSubmitDraft(t, d, seed.ID, 2)
	d.gardenBroadcastHook = func([]protocol.Seed, int) {
		if err := d.store.Close(); err != nil {
			t.Errorf("closing store after note: %v", err)
		}
	}

	res := sendSeedSubmit(t, d, seed.ID, nil, protocol.Ptr(seed.ID))

	if !res.Success || res.Status != annotationSubmitStatusNoted {
		t.Fatalf("result = %+v, want noted despite clear failure", res)
	}
	if res.Error == nil || !strings.Contains(*res.Error, "noted; failed to clear drafts") {
		t.Fatalf("error = %v, want noted-but-not-cleared marker", res.Error)
	}
	if res.Generation != nil {
		t.Fatalf("generation should be absent when the clear failed, got %v", res.Generation)
	}
}

func TestMarkdownAnnotationsSubmitDeliveryFailure(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	d.ptyBackend = &failingInputBackend{fakeSpawnBackend: &fakeSpawnBackend{}}
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "target", nil)

	if res.Success || res.Status != annotationSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "pty write exploded") {
		t.Fatalf("result = %+v, want delivery error", res)
	}
	if n := storedSubmitDraftCount(t, d); n != 1 {
		t.Fatalf("draft must stay intact when delivery fails, got %d annotations", n)
	}
}

func TestMarkdownAnnotationsSubmitClearFailureStillDelivered(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	d.ptyBackend = &fakeSpawnBackend{onInput: func(string, []byte) {
		if err := d.store.Close(); err != nil {
			t.Errorf("closing store: %v", err)
		}
	}}
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "target", nil)

	if !res.Success || res.Status != annotationSubmitStatusDelivered {
		t.Fatalf("result = %+v, want delivered despite clear failure", res)
	}
	if res.Error == nil || !strings.Contains(*res.Error, "delivered; failed to clear drafts") {
		t.Fatalf("error = %v, want delivered-but-not-cleared marker", res.Error)
	}
	if res.Generation != nil {
		t.Fatalf("generation should be absent when the clear failed, got %v", res.Generation)
	}
}

type failingInputBackend struct {
	*fakeSpawnBackend
}

func (b *failingInputBackend) Input(context.Context, string, []byte) error {
	return fmt.Errorf("pty write exploded")
}
