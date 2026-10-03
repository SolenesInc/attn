package daemon_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const sessionAnnotationFeedback = "Feedback on your last message.\n\n## 1. 🔍 Verify this\n\n> the parser already handles this"

func TestSubmittedAnnotationsReachTheAgentAfterWhatTheUserTyped(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")
	plan := fileAnnotatedDocument("/notes/plan.md")
	hello := []protocol.MarkdownAnnotation{{ID: "c1", Type: "comment", Text: protocol.Ptr("hi"), CreatedAt: 1,
		Anchor: &protocol.MarkdownAnnotationAnchor{BlockID: "b", StartLine: 3, EndLine: 3, Start: 0, End: 5, Exact: "hello"}}}
	planPayload := func(label string) string {
		return "# Markdown Annotations\n\nFile: /notes/plan.md\n\nI've reviewed this document and have 1 piece of feedback:\n\n" +
			"## 1. (" + label + ") Feedback on: \"hello\"\n> hi\n\n---\nPlease address the annotation feedback above."
	}

	for _, tc := range []struct {
		name           string
		typed          string
		submit         func() annotationSubmitOutcome
		want           string
		wantGeneration *int
	}{
		{"session feedback", "", func() annotationSubmitOutcome {
			return submitSessionAnnotationFeedback(app, session, sessionAnnotationFeedback)
		}, sessionAnnotationFeedback, nil},
		{"session feedback over words the user typed", "existing words",
			func() annotationSubmitOutcome {
				return submitSessionAnnotationFeedback(app, session, sessionAnnotationFeedback)
			}, "existing words" + sessionAnnotationFeedback, nil},
		{"document annotations", "", func() annotationSubmitOutcome {
			saveDocumentAnnotations(app, plan, 5, hello)
			return submitDocumentAnnotations(app, plan, session, "", nil)
		}, planPayload("line 3"), protocol.Ptr(5)},
		{"moved document annotations over words the user typed", "existing words", func() annotationSubmitOutcome {
			saveDocumentAnnotations(app, plan, 7, hello)
			return submitDocumentAnnotations(app, plan, session, "", []string{"c1"})
		}, "existing words" + planPayload("~line 3, moved"), protocol.Ptr(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.typed != "" {
				probe := uuid.NewString()
				testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: app.Terminal(session), Data: tc.typed, ProbeID: protocol.Ptr(probe)},
					protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
			}
			if got := tc.submit(); !got.success || got.status != "delivered" || got.err != "" || !reflect.DeepEqual(got.generation, tc.wantGeneration) {
				t.Fatalf("submit = %+v (generation %v), want delivered at generation %v", got, protocol.Deref(got.generation), protocol.Deref(tc.wantGeneration))
			}
			if got := agent.Prompted(); got != tc.want {
				t.Errorf("the agent was prompted with %q, want %q", got, tc.want)
			}
			agent.Reply("Noted. <!-- attn:state=idle -->")
		})
	}

	if got := getDocumentAnnotations(app, plan); len(got.Annotations) != 0 || got.Generation != 7 {
		t.Errorf("the delivered draft = %+v, want it cleared at floor 7", got)
	}
	if stale := saveDocumentAnnotations(app, plan, 7, hello); stale.Success || !protocol.Deref(stale.Stale) {
		t.Errorf("a save at the delivered generation = %+v, want it refused as stale", stale)
	}
}

func TestADocumentsAnnotationsReachTheAgentOrderedByPlaceWithGeneralFeedbackLastAndLabelsCounted(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")
	doc := fileAnnotatedDocument("/doc.md")
	anchor := func(startLine, endLine, start int, exact string) *protocol.MarkdownAnnotationAnchor {
		return &protocol.MarkdownAnnotationAnchor{BlockID: "b", StartLine: startLine, EndLine: endLine, Start: start, End: start + len(exact), Exact: exact}
	}
	looksGood := func(id string, line int, quote string) protocol.MarkdownAnnotation {
		return protocol.MarkdownAnnotation{ID: id, Type: "comment", Anchor: anchor(line, line, 0, quote),
			QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"), QuickLabelTip: protocol.Ptr("nice"), CreatedAt: line}
	}
	const header = "# Markdown Annotations\n\nFile: /doc.md\n\n"
	const closing = "---\nPlease address the annotation feedback above."

	for i, tc := range []struct {
		name  string
		marks []protocol.MarkdownAnnotation
		want  string
	}{
		{
			name: "every kind, ordered by line with general feedback last",
			marks: []protocol.MarkdownAnnotation{
				{ID: "del", Type: "deletion", Anchor: anchor(30, 30, 0, "old paragraph"), CreatedAt: 1},
				{ID: "glob", Type: "global", Text: protocol.Ptr("a global comment"), CreatedAt: 2},
				{ID: "range", Type: "comment", Anchor: anchor(12, 18, 4, "the selected text"), Text: protocol.Ptr("the reviewer's comment"), CreatedAt: 3},
				{ID: "ql", Type: "comment", Anchor: anchor(5, 5, 2, "selected text"),
					QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"), QuickLabelTip: protocol.Ptr("Keep more of this"), CreatedAt: 4},
			},
			want: header + "I've reviewed this document and have 4 pieces of feedback:\n\n" +
				"## 1. (line 5) [👍 Looks good] Feedback on: \"selected text\"\n> Keep more of this\n\n" +
				"## 2. (lines 12–18) Feedback on: \"the selected text\"\n> the reviewer's comment\n\n" +
				"## 3. (line 30) Remove this\n```\nold paragraph\n```\n> I don't want this in the document.\n\n" +
				"## 4. General feedback about the document\n> a global comment\n\n" +
				"---\n## Label Summary\n\n- **👍 Looks good**: 1\n\nPlease address the annotation feedback above.",
		},
		{
			name: "general feedback in the order it was written",
			marks: []protocol.MarkdownAnnotation{
				{ID: "later", Type: "global", Text: protocol.Ptr("second overall note"), CreatedAt: 20},
				{ID: "earlier", Type: "global", Text: protocol.Ptr("first overall note"), CreatedAt: 10},
			},
			want: header + "I've reviewed this document and have 2 pieces of feedback:\n\n" +
				"## 1. General feedback about the document\n> first overall note\n\n" +
				"## 2. General feedback about the document\n> second overall note\n\n" + closing,
		},
		{
			name:  "quick labels with their tips, counted in the order first seen",
			marks: []protocol.MarkdownAnnotation{looksGood("a", 1, "one"), {ID: "b", Type: "comment", Anchor: anchor(2, 2, 0, "two"), QuickLabelID: protocol.Ptr("confusing"), CreatedAt: 2}, looksGood("c", 3, "three")},
			want: header + "I've reviewed this document and have 3 pieces of feedback:\n\n" +
				"## 1. (line 1) [👍 Looks good] Feedback on: \"one\"\n> nice\n\n" +
				"## 2. (line 2) [confusing] Feedback on: \"two\"\n\n" +
				"## 3. (line 3) [👍 Looks good] Feedback on: \"three\"\n> nice\n\n" +
				"---\n## Label Summary\n\n- **👍 Looks good**: 2\n- **confusing**: 1\n\nPlease address the annotation feedback above.",
		},
		{
			name: "two comments on one line, in the order of where they start",
			marks: []protocol.MarkdownAnnotation{
				{ID: "later", Type: "comment", Anchor: anchor(4, 4, 20, "tail"), Text: protocol.Ptr("second"), CreatedAt: 1},
				{ID: "earlier", Type: "comment", Anchor: anchor(4, 4, 2, "head"), Text: protocol.Ptr("first"), CreatedAt: 2},
			},
			want: header + "I've reviewed this document and have 2 pieces of feedback:\n\n" +
				"## 1. (line 4) Feedback on: \"head\"\n> first\n\n" +
				"## 2. (line 4) Feedback on: \"tail\"\n> second\n\n" + closing,
		},
		{
			name:  "a comment without an anchor",
			marks: []protocol.MarkdownAnnotation{{ID: "x", Type: "comment", Text: protocol.Ptr("dangling"), CreatedAt: 1}},
			want:  header + "I've reviewed this document and have 1 piece of feedback:\n\n## 1. Feedback on: \"\"\n> dangling\n\n" + closing,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveDocumentAnnotations(app, doc, i+1, tc.marks)
			if got := submitDocumentAnnotations(app, doc, session, "", nil); got.status != "delivered" {
				t.Fatalf("submit = %+v, want delivered", got)
			}
			if got := agent.Prompted(); got != tc.want {
				t.Errorf("the agent was prompted with\n%s\nwant\n%s", got, tc.want)
			}
			agent.Reply("Noted. <!-- attn:state=idle -->")
		})
	}
}

func TestAnnotationsThatCannotBeDeliveredTypeNothingAndKeepTheDraft(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")
	plan, blank := fileAnnotatedDocument("/notes/plan.md"), fileAnnotatedDocument("/notes/blank.md")
	marks := []protocol.MarkdownAnnotation{{ID: "g1", Type: "global", Text: protocol.Ptr("tighten the intro"), CreatedAt: 1}}
	saveDocumentAnnotations(app, plan, 2, marks)

	if err := cli.RecordNotification(session, "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	for _, tc := range []struct {
		name       string
		submit     func() annotationSubmitOutcome
		wantStatus string
		wantError  string
	}{
		{"feedback for a session waiting on approval", func() annotationSubmitOutcome {
			return submitSessionAnnotationFeedback(app, session, sessionAnnotationFeedback)
		},
			"skipped_pending_approval", ""},
		{"a document for a session waiting on approval", func() annotationSubmitOutcome { return submitDocumentAnnotations(app, plan, session, "", nil) },
			"skipped_pending_approval", ""},
		{"feedback for an unknown session", func() annotationSubmitOutcome {
			return submitSessionAnnotationFeedback(app, "nope", sessionAnnotationFeedback)
		},
			"error", "unknown session nope"},
		{"blank feedback", func() annotationSubmitOutcome { return submitSessionAnnotationFeedback(app, session, "   \n ") },
			"error", "text is required"},
		{"a document for an unknown session", func() annotationSubmitOutcome { return submitDocumentAnnotations(app, plan, "nope", "", nil) },
			"error", "session not found: nope"},
		{"an empty draft", func() annotationSubmitOutcome { return submitDocumentAnnotations(app, blank, session, "", nil) },
			"error", "no annotations to send"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.submit()
			if got.success || got.status != tc.wantStatus || !strings.Contains(got.err, tc.wantError) || (tc.wantError == "" && got.err != "") {
				t.Errorf("submit = %+v, want %s naming %q", got, tc.wantStatus, tc.wantError)
			}
		})
	}

	if got := getDocumentAnnotations(app, plan); len(got.Annotations) != 1 || got.Generation != 2 {
		t.Errorf("the undelivered draft = %+v, want it kept at generation 2", got)
	}
	if err := cli.UpdateState(session, protocol.StateWorking); err != nil {
		t.Fatalf("approve: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if delivered := submitSessionAnnotationFeedback(app, session, "after the approval"); delivered.status != "delivered" {
		t.Fatalf("feedback after the approval = %+v, want delivered", delivered)
	}
	if got := agent.Prompted(); got != "after the approval" {
		t.Errorf("the agent's first prompt was %q, want only the feedback sent after the approval", got)
	}
}

func TestAnnotationsTheAgentsTerminalWillNotTakeKeepTheDraft(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	app.AwaitScreen(session, "? for shortcuts")
	plan := fileAnnotatedDocument("/notes/plan.md")
	marks := []protocol.MarkdownAnnotation{{ID: "g1", Type: "global", Text: protocol.Ptr(strings.Repeat("tighten the intro ", 16<<10)), CreatedAt: 1}}
	saveDocumentAnnotations(app, plan, 3, marks)
	agent.StopReadingTerminal()

	if got := submitDocumentAnnotations(app, plan, session, "", nil); got.success || got.status != "error" || got.err == "" {
		t.Errorf("submit to an agent that stopped reading its terminal = %+v, want an error naming the failed delivery", got)
	}
	if draft := getDocumentAnnotations(app, plan); len(draft.Annotations) != 1 || draft.Generation != 3 {
		t.Errorf("the undelivered draft = %+v, want it kept at generation 3", draft)
	}
	agent.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == session })
}

func TestSeedAnnotationsBecomeANoteOnThatSeedOnly(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	seed := plantSeed(t, w, "Review target")
	reviewed, plan := seedAnnotatedDocument(seed.ID), fileAnnotatedDocument("/notes/plan.md")
	marks := []protocol.MarkdownAnnotation{{ID: "g1", Type: "global", Text: protocol.Ptr("split the rollout"), CreatedAt: 1}}
	saveDocumentAnnotations(app, reviewed, 4, marks)
	saveDocumentAnnotations(app, plan, 2, marks)

	for _, tc := range []struct {
		name    string
		doc     annotatedDocument
		session string
		seed    string
		want    string
	}{
		{"no destination", reviewed, "", "", "exactly one"},
		{"both destinations", reviewed, "sess-a", seed.ID, "exactly one"},
		{"another seed", reviewed, "", "s-ffffff", "must match"},
		{"a file document to a seed", plan, "", seed.ID, "must match the seed document source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := submitDocumentAnnotations(app, tc.doc, tc.session, tc.seed, nil)
			if refused.success || !strings.Contains(refused.err, tc.want) {
				t.Errorf("submit = %+v, want it refused naming %q", refused, tc.want)
			}
		})
	}
	for _, doc := range []annotatedDocument{reviewed, plan} {
		if got := getDocumentAnnotations(app, doc); len(got.Annotations) != 1 {
			t.Errorf("draft of %s after the refused routes = %+v, want it intact", doc.uri, got)
		}
	}

	noted := submitDocumentAnnotations(app, reviewed, "", seed.ID, nil)
	if !noted.success || noted.status != "noted" || noted.err != "" || protocol.Deref(noted.targetSeed) != seed.ID || noted.targetSession != nil {
		t.Fatalf("submitting the seed's annotations to it = %+v, want noted on %s", noted, seed.ID)
	}
	shown, err := cli.SeedShow("", seed.ID)
	if err != nil {
		t.Fatalf("show %s: %v", seed.ID, err)
	}
	if shown.NotesTotal != 1 || len(shown.Notes) != 1 {
		t.Fatalf("the seed's log = %+v (total %d), want one note", shown.Notes, shown.NotesTotal)
	}
	note := shown.Notes[0]
	if note.Kind != "note" || !strings.Contains(note.Body, "Seed: "+seed.ID+" — Review target") || !strings.Contains(note.Body, "> split the rollout") {
		t.Errorf("the note = %+v, want it to name the seed and carry the annotation", note)
	}
	if got := getDocumentAnnotations(app, reviewed); len(got.Annotations) != 0 {
		t.Errorf("the noted draft = %+v, want it cleared", got)
	}
}

type annotatedDocument struct {
	uri, kind string
	path      *string
	seedID    *string
}

func fileAnnotatedDocument(path string) annotatedDocument {
	uri, filePath := annotationSource(path)
	return annotatedDocument{uri: uri, kind: "file", path: filePath}
}

func seedAnnotatedDocument(seedID string) annotatedDocument {
	return annotatedDocument{uri: "attn://seed/" + seedID, kind: "seed", seedID: protocol.Ptr(seedID)}
}

func saveDocumentAnnotations(app *testworld.Peer, doc annotatedDocument, generation int, marks []protocol.MarkdownAnnotation) protocol.MarkdownAnnotationsSaveResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: requestID, DocumentUri: doc.uri, SourceKind: doc.kind,
		Path: doc.path, SeedID: doc.seedID, Generation: generation, Annotations: marks,
	}, protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.RequestID == requestID })
}

func getDocumentAnnotations(app *testworld.Peer, doc annotatedDocument) protocol.MarkdownAnnotationsGetResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{
		Cmd: protocol.CmdMarkdownAnnotationsGet, RequestID: requestID, DocumentUri: doc.uri, SourceKind: doc.kind,
		Path: doc.path, SeedID: doc.seedID,
	}, protocol.EventMarkdownAnnotationsGetResult, func(r protocol.MarkdownAnnotationsGetResultMessage) bool { return r.RequestID == requestID })
}

type annotationSubmitOutcome struct {
	success                   bool
	status, err               string
	generation                *int
	targetSeed, targetSession *string
}

func submitDocumentAnnotations(app *testworld.Peer, doc annotatedDocument, targetSession, targetSeed string, orphaned []string) annotationSubmitOutcome {
	app.T.Helper()
	msg := protocol.MarkdownAnnotationsSubmitMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSubmit, RequestID: uuid.NewString(), DocumentUri: doc.uri, SourceKind: doc.kind,
		Path: doc.path, SeedID: doc.seedID, OrphanedIds: orphaned,
	}
	if targetSession != "" {
		msg.TargetSessionID = protocol.Ptr(targetSession)
	}
	if targetSeed != "" {
		msg.TargetSeedID = protocol.Ptr(targetSeed)
	}
	r := testworld.Request(app, msg, protocol.EventMarkdownAnnotationsSubmitResult,
		func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.RequestID == msg.RequestID })
	return annotationSubmitOutcome{success: r.Success, status: r.Status, err: protocol.Deref(r.Error), generation: r.Generation,
		targetSeed: r.TargetSeedID, targetSession: r.TargetSessionID}
}

func submitSessionAnnotationFeedback(app *testworld.Peer, sessionID, text string) annotationSubmitOutcome {
	app.T.Helper()
	requestID := uuid.NewString()
	r := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: sessionID, Text: text,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
	return annotationSubmitOutcome{success: r.Success, status: r.Status, err: protocol.Deref(r.Error)}
}
