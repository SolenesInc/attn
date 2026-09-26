package daemon_test

import (
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClearingMarkdownAnnotationsNeverLowersTheFloorAndStaysWithItsFile(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	const plan, other, fresh = "/notes/plan.md", "/notes/other.md", "/notes/fresh.md"

	if saved := saveAnnotations(app, other, 1, "keep me"); !saved.Success {
		t.Fatalf("save %s = %+v", other, saved)
	}
	if saved := saveAnnotations(app, plan, 7, "first pass"); !saved.Success {
		t.Fatalf("save %s = %+v", plan, saved)
	}
	if cleared := clearAnnotations(app, plan, 2); !cleared.Success || cleared.Generation != 7 {
		t.Fatalf("a late clear at generation 2 = %+v, want success with the floor kept at 7", cleared)
	}
	if ghost := saveAnnotations(app, plan, 7, "ghost"); ghost.Success || !protocol.Deref(ghost.Stale) {
		t.Errorf("a save at generation 7 after the clear = %+v, want it refused as stale", ghost)
	}
	if cleared := clearAnnotations(app, plan, 1); !cleared.Success || cleared.Generation != 7 {
		t.Errorf("an older clear = %+v, want success with the floor kept at 7", cleared)
	}
	if got := getAnnotations(app, plan); len(got.Annotations) != 0 || got.Generation != 7 {
		t.Errorf("%s after the clears = %+v, want no annotations at floor 7", plan, got)
	}

	for range 2 {
		if cleared := clearAnnotations(app, fresh, 3); !cleared.Success || cleared.Generation != 3 {
			t.Errorf("clearing a never-annotated file = %+v, want success at floor 3", cleared)
		}
	}
	if got := getAnnotations(app, fresh); len(got.Annotations) != 0 || got.Generation != 3 {
		t.Errorf("%s after two clears = %+v, want no annotations at floor 3", fresh, got)
	}

	if got := getAnnotations(app, other); got.Generation != 1 || len(got.Annotations) != 1 || protocol.Deref(got.Annotations[0].Text) != "keep me" {
		t.Errorf("%s = %+v, want its own draft untouched by the other files' clears", other, got)
	}
}

func annotationSource(path string) (documentURI string, workspaceID, filePath *string) {
	return "attn://file/workspace-notes/" + url.PathEscape(path), protocol.Ptr("workspace-notes"), protocol.Ptr(path)
}

func saveAnnotations(app *testworld.Peer, path string, generation int, text string) protocol.MarkdownAnnotationsSaveResultMessage {
	app.T.Helper()
	uri, workspaceID, filePath := annotationSource(path)
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: requestID, DocumentUri: uri, SourceKind: "file",
		WorkspaceID: workspaceID, Path: filePath, Generation: generation,
		Annotations: []protocol.MarkdownAnnotation{{ID: uuid.NewString(), Type: "global", Text: protocol.Ptr(text), CreatedAt: 1}},
	}, protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.RequestID == requestID })
}

func clearAnnotations(app *testworld.Peer, path string, generation int) protocol.MarkdownAnnotationsClearResultMessage {
	app.T.Helper()
	uri, workspaceID, filePath := annotationSource(path)
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.MarkdownAnnotationsClearMessage{
		Cmd: protocol.CmdMarkdownAnnotationsClear, RequestID: requestID, DocumentUri: uri, SourceKind: "file",
		WorkspaceID: workspaceID, Path: filePath, Generation: generation,
	}, protocol.EventMarkdownAnnotationsClearResult, func(r protocol.MarkdownAnnotationsClearResultMessage) bool { return r.RequestID == requestID })
}

func getAnnotations(app *testworld.Peer, path string) protocol.MarkdownAnnotationsGetResultMessage {
	app.T.Helper()
	uri, workspaceID, filePath := annotationSource(path)
	requestID := uuid.NewString()
	got := testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{
		Cmd: protocol.CmdMarkdownAnnotationsGet, RequestID: requestID, DocumentUri: uri, SourceKind: "file",
		WorkspaceID: workspaceID, Path: filePath,
	}, protocol.EventMarkdownAnnotationsGetResult, func(r protocol.MarkdownAnnotationsGetResultMessage) bool { return r.RequestID == requestID })
	if !got.Success {
		app.T.Fatalf("get annotations for %s refused: %s", path, protocol.Deref(got.Error))
	}
	return got
}
