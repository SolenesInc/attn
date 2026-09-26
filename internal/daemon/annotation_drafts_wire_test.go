package daemon_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestMarkdownAnnotationDraftsKeepEveryFieldAndRefuseADraftWithoutADocument(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	nowhere := fileAnnotatedDocument("   ")
	nowhere.uri = "attn://file/workspace-notes"

	requestID := uuid.NewString()
	if got := testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{
		Cmd: protocol.CmdMarkdownAnnotationsGet, RequestID: requestID, DocumentUri: nowhere.uri, SourceKind: "file", WorkspaceID: nowhere.workspaceID, Path: nowhere.path,
	}, protocol.EventMarkdownAnnotationsGetResult, func(r protocol.MarkdownAnnotationsGetResultMessage) bool { return r.RequestID == requestID }); got.Success || got.Error == nil {
		t.Errorf("get without a document = %+v, want an error result", got)
	}
	if got := saveDocumentAnnotations(app, nowhere, 1, nil); got.Success || got.Error == nil {
		t.Errorf("save without a document = %+v, want an error result", got)
	}
	requestID = uuid.NewString()
	if got := testworld.Request(app, protocol.MarkdownAnnotationsClearMessage{
		Cmd: protocol.CmdMarkdownAnnotationsClear, RequestID: requestID, DocumentUri: nowhere.uri, SourceKind: "file", WorkspaceID: nowhere.workspaceID, Path: nowhere.path, Generation: 1,
	}, protocol.EventMarkdownAnnotationsClearResult, func(r protocol.MarkdownAnnotationsClearResultMessage) bool { return r.RequestID == requestID }); got.Success || got.Error == nil {
		t.Errorf("clear without a document = %+v, want an error result", got)
	}

	plan := fileAnnotatedDocument("/notes/plan.md")
	if got := getDocumentAnnotations(app, plan); !got.Success || len(got.Annotations) != 0 || got.Generation != 0 {
		t.Fatalf("a never-annotated file = %+v, want an empty draft at generation 0", got)
	}
	marks := []protocol.MarkdownAnnotation{
		{ID: "a1", Type: "comment", Text: protocol.Ptr("tighten this"), CreatedAt: 1752494400000, Anchor: &protocol.MarkdownAnnotationAnchor{
			BlockID: "b-3", StartLine: 10, EndLine: 12, Exact: "the exact text", Prefix: "pre", Suffix: "post", Start: 4, End: 18, ContentHash: "abc123",
		}},
		{ID: "a2", Type: "comment", QuickLabelID: protocol.Ptr("verify-this"), QuickLabelTip: protocol.Ptr("Verify by reading the code."), CreatedAt: 1752494401000},
		{ID: "a3", Type: "global", Text: protocol.Ptr("overall: solid"), CreatedAt: 1752494402000},
	}
	padded := plan
	padded.path = protocol.Ptr("  /notes/plan.md  ")
	saved := saveDocumentAnnotations(app, padded, 1, marks)
	if !saved.Success || saved.Stale != nil || protocol.Deref(saved.Path) != "/notes/plan.md" || saved.Generation != 1 {
		t.Fatalf("save = %+v, want success on the trimmed path at generation 1", saved)
	}
	if got := getDocumentAnnotations(app, plan); !reflect.DeepEqual(got.Annotations, marks) || got.Generation != 1 {
		t.Errorf("draft = %+v, want every saved field back at generation 1", got)
	}
}

func TestSessionAnnotationsSurviveARestartWithTheirSessionAndGoWhenItIsPruned(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	talked := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(talked)
	app.TypeLine(talked, "add a discount field to checkout")
	agent.Prompted()
	agent.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, talked, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	untouched := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(untouched)
	marks := []protocol.SessionAnnotation{
		{ID: "a1", MessageKey: "msg-old", End: 15, Quote: "an earlier turn", QuickLabelID: protocol.Ptr("consider-alternatives")},
		{ID: "a2", MessageKey: "msg-new", End: 15, Quote: "the newest turn", QuickLabelID: protocol.Ptr("consider-alternatives")},
	}
	for _, session := range []string{talked, untouched} {
		if saved := saveSessionAnnotations(app, session, 1, marks, "Split this into two PRs."); !saved.Success {
			t.Fatalf("save for %s = %+v", session, saved)
		}
	}

	w.restart()
	app = w.App()

	if got := getSessionAnnotations(app, untouched); len(got.Annotations) != 0 || got.Note != nil || got.Generation != 0 {
		t.Errorf("draft of the pruned session = %+v, want it gone back to generation 0", got)
	}
	got := getSessionAnnotations(app, talked)
	if !reflect.DeepEqual(got.Annotations, marks) || protocol.Deref(got.Note) != "Split this into two PRs." || got.Generation != 1 {
		t.Fatalf("draft of the recoverable session = %+v, want both marks in order with the note at generation 1", got)
	}
	if saved := saveSessionAnnotations(app, talked, 2, marks[:1], ""); !saved.Success {
		t.Fatalf("save without a note = %+v", saved)
	}
	if got := getSessionAnnotations(app, talked); len(got.Annotations) != 1 || got.Note != nil {
		t.Errorf("draft saved without a note = %+v, want one mark and no note", got)
	}
	saveSessionAnnotations(app, talked, 3, marks, "Keep the old import path.")
	if cleared := clearSessionAnnotations(app, talked, 4); !cleared.Success {
		t.Fatalf("clear = %+v", cleared)
	}
	if got := getSessionAnnotations(app, talked); len(got.Annotations) != 0 || got.Note != nil || got.Generation != 4 {
		t.Errorf("draft after the clear = %+v, want no marks and no note at generation 4", got)
	}
}

func TestSessionAnnotationDraftsNeedASessionID(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, id := range []string{"", "   "} {
		if got := getSessionAnnotations(app, id); got.Success || protocol.Deref(got.Error) == "" {
			t.Errorf("get for session %q = %+v, want an error result", id, got)
		}
		if got := saveSessionAnnotations(app, id, 1, nil, ""); got.Success || protocol.Deref(got.Error) == "" {
			t.Errorf("save for session %q = %+v, want an error result", id, got)
		}
		if got := clearSessionAnnotations(app, id, 1); got.Success || protocol.Deref(got.Error) == "" {
			t.Errorf("clear for session %q = %+v, want an error result", id, got)
		}
	}
}
