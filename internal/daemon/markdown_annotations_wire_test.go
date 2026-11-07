package daemon_test

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReadersChangeADraftMarkByMark(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	plan, other := fileAnnotatedDocument("/notes/plan.md"), fileAnnotatedDocument("/notes/other.md")
	note := func(id, text string) protocol.MarkdownAnnotation {
		return protocol.MarkdownAnnotation{ID: id, Type: "global", Text: protocol.Ptr(text), CreatedAt: 1}
	}
	change := func(doc annotatedDocument, upserts []protocol.MarkdownAnnotation, removed ...string) protocol.MarkdownAnnotationsSaveResultMessage {
		t.Helper()
		requestID := uuid.NewString()
		return testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
			Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: requestID, DocumentUri: doc.uri, SourceKind: doc.kind, Path: doc.path,
			Upserts: upserts, RemovedIds: append([]string{}, removed...),
		}, protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.RequestID == requestID })
	}

	change(other, []protocol.MarkdownAnnotation{note("x", "keep me")})
	first := change(plan, []protocol.MarkdownAnnotation{note("a", "first"), note("b", "second")})
	if want := []protocol.MarkdownAnnotation{note("a", "first"), note("b", "second")}; !first.Success || !reflect.DeepEqual(first.Annotations, want) || first.Generation != 1 {
		t.Fatalf("the first save = %+v, want both marks at generation 1", first)
	}

	merged := change(plan, []protocol.MarkdownAnnotation{note("c", "from another reader"), note("a", "rewritten")}, "b")
	want := []protocol.MarkdownAnnotation{note("a", "rewritten"), note("c", "from another reader")}
	if !merged.Success || !reflect.DeepEqual(merged.Annotations, want) || merged.Generation != 2 {
		t.Errorf("a save changing some marks = %+v, want %+v at generation 2", merged, want)
	}

	retried := change(plan, []protocol.MarkdownAnnotation{note("c", "from another reader"), note("a", "rewritten")}, "b")
	if !retried.Success || !reflect.DeepEqual(retried.Annotations, want) || retried.Generation != 2 {
		t.Errorf("the same save again = %+v, want nothing changed at generation 2", retried)
	}

	emptied := change(plan, nil, "a", "c")
	if !emptied.Success || len(emptied.Annotations) != 0 || emptied.Annotations == nil || emptied.Generation != 3 {
		t.Errorf("removing every mark = %+v, want an empty list at generation 3", emptied)
	}
	if got := getDocumentAnnotations(app, plan); len(got.Annotations) != 0 || got.Generation != 3 {
		t.Errorf("%s = %+v, want it empty at generation 3", *plan.path, got)
	}
	if got := getDocumentAnnotations(app, other); got.Generation != 1 || len(got.Annotations) != 1 || protocol.Deref(got.Annotations[0].Text) != "keep me" {
		t.Errorf("%s = %+v, want its own draft untouched", *other.path, got)
	}
}

func TestASendKeepsWhatTwoReadersSavedBeforeItsReplyArrived(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	planted, err := cli.SeedPlant("", "Reviewed child", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seed := planted.Seed.ID
	doc := annotatedDocument{uri: "attn://seed/" + seed, kind: "seed", seedID: protocol.Ptr(seed)}
	note := func(id string) protocol.MarkdownAnnotation {
		return protocol.MarkdownAnnotation{ID: id, Type: "global", Text: protocol.Ptr(id + " note"), CreatedAt: 1}
	}
	saveDocumentAnnotations(app, doc, []protocol.MarkdownAnnotation{note("a")})

	submitID := uuid.NewString()
	app.Send(protocol.MarkdownAnnotationsSubmitMessage{Cmd: protocol.CmdMarkdownAnnotationsSubmit, RequestID: submitID,
		DocumentUri: doc.uri, SourceKind: doc.kind, SeedID: doc.seedID, TargetSeedID: doc.seedID})
	saveDocumentAnnotations(app, doc, []protocol.MarkdownAnnotation{note("c")})
	saveDocumentAnnotations(app, doc, []protocol.MarkdownAnnotation{note("b")})

	sent := testworld.Await(app, protocol.EventMarkdownAnnotationsSubmitResult, func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.RequestID == submitID })
	if sent.Status != "noted" {
		t.Fatalf("the send = %+v, want it noted", sent)
	}
	notes := lifeNoteBodies(t, cli, seed)
	if len(notes) != 1 || !strings.Contains(notes[0], "a note") || strings.Contains(notes[0], "b note") || strings.Contains(notes[0], "c note") {
		t.Errorf("the seed's log = %q, want one note carrying only the first reader's sent mark", notes)
	}
	want := []protocol.MarkdownAnnotation{note("c"), note("b")}
	if got := getDocumentAnnotations(app, doc); !reflect.DeepEqual(got.Annotations, want) {
		t.Errorf("the draft = %+v, want both unsent marks and not the sent one", got.Annotations)
	}
}

func annotationSource(path string) (documentURI string, filePath *string) {
	return "attn://file/" + strings.ReplaceAll(url.QueryEscape(path), "+", "%20"), protocol.Ptr(path)
}
