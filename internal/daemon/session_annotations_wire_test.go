package daemon_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSessionAnnotationDraftsKeepTheNewestGenerationAndStayClearedAfterAClear(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	reviewed := w.Spawn(app, fakeagent.Codex, w.Path("api"))
	neighbour := w.Spawn(app, fakeagent.Codex, w.Path("web"))
	marks := []protocol.SessionAnnotation{{ID: "a1", MessageKey: "turn-1", Start: 4, End: 10, Quote: "parser", Emoji: protocol.Ptr("❓"), Comment: "why this?"}}

	if got := getSessionAnnotations(app, reviewed); !got.Success || len(got.Annotations) != 0 || got.Generation != 0 || protocol.Deref(got.Note) != "" {
		t.Fatalf("a never-annotated session = %+v, want an empty draft at generation 0", got)
	}
	if cleared := clearSessionAnnotations(app, reviewed, 3); !cleared.Success {
		t.Fatalf("clearing a never-annotated session = %+v", cleared)
	}
	if late := saveSessionAnnotations(app, reviewed, 3, marks, ""); late.Success || !protocol.Deref(late.Stale) {
		t.Errorf("a save at the generation a never-annotated session was cleared at = %+v, want it refused", late)
	}

	if saved := saveSessionAnnotations(app, reviewed, 4, marks, "Split this into two PRs."); !saved.Success || saved.Generation != 4 {
		t.Fatalf("save at generation 4 = %+v", saved)
	}
	got := getSessionAnnotations(app, reviewed)
	if !reflect.DeepEqual(got.Annotations, marks) || got.Generation != 4 || protocol.Deref(got.Note) != "Split this into two PRs." {
		t.Fatalf("draft after the save = %+v, want the marks and note at generation 4", got)
	}
	if other := getSessionAnnotations(app, neighbour); len(other.Annotations) != 0 || other.Generation != 0 || protocol.Deref(other.Note) != "" {
		t.Errorf("the other session's draft = %+v, want it untouched by the first session's save", other)
	}

	if stale := saveSessionAnnotations(app, reviewed, 3, nil, "the older note"); stale.Success || !protocol.Deref(stale.Stale) || stale.Error != nil {
		t.Errorf("a save at generation 3 after 4 = %+v, want it refused as stale without an error", stale)
	}
	if got := getSessionAnnotations(app, reviewed); !reflect.DeepEqual(got.Annotations, marks) || protocol.Deref(got.Note) != "Split this into two PRs." {
		t.Errorf("draft after the stale save = %+v, want the newer marks and note untouched", got)
	}

	if cleared := clearSessionAnnotations(app, reviewed, 5); !cleared.Success || cleared.Generation != 5 {
		t.Fatalf("clear at generation 5 = %+v", cleared)
	}
	if inFlight := saveSessionAnnotations(app, reviewed, 5, marks, "ghost"); inFlight.Success || !protocol.Deref(inFlight.Stale) {
		t.Errorf("a save at the cleared generation = %+v, want it refused so the cleared marks stay gone", inFlight)
	}
	if got := getSessionAnnotations(app, reviewed); len(got.Annotations) != 0 || protocol.Deref(got.Note) != "" || got.Generation != 5 {
		t.Errorf("draft after the clear = %+v, want no marks and no note at generation 5", got)
	}
}

func saveSessionAnnotations(app *testworld.Peer, sessionID string, generation int, marks []protocol.SessionAnnotation, note string) protocol.SessionAnnotationsSaveResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SessionAnnotationsSaveMessage{
		Cmd: protocol.CmdSessionAnnotationsSave, RequestID: requestID, SessionID: sessionID,
		Generation: generation, Annotations: marks, Note: protocol.Ptr(note),
	}, protocol.EventSessionAnnotationsSaveResult, func(r protocol.SessionAnnotationsSaveResultMessage) bool { return r.RequestID == requestID })
}

func clearSessionAnnotations(app *testworld.Peer, sessionID string, generation int) protocol.SessionAnnotationsClearResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SessionAnnotationsClearMessage{
		Cmd: protocol.CmdSessionAnnotationsClear, RequestID: requestID, SessionID: sessionID, Generation: generation,
	}, protocol.EventSessionAnnotationsClearResult, func(r protocol.SessionAnnotationsClearResultMessage) bool { return r.RequestID == requestID })
}

func getSessionAnnotations(app *testworld.Peer, sessionID string) protocol.SessionAnnotationsGetResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SessionAnnotationsGetMessage{
		Cmd: protocol.CmdSessionAnnotationsGet, RequestID: requestID, SessionID: sessionID,
	}, protocol.EventSessionAnnotationsGetResult, func(r protocol.SessionAnnotationsGetResultMessage) bool { return r.RequestID == requestID })
}
