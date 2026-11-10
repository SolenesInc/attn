package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const calcReviewBody = "# Name the sum\n\nAdd keeps its result in a variable.\n\n## Add keeps its result\n\n### calc.go:3-6\n\n- L4!: The sum gets a name.\n"

type openReview struct {
	w        *world
	app      *testworld.Peer
	session  string
	agent    *fakeagent.Run
	document string
	doc      annotatedDocument
	repo     string
	head     string
}

func openCalcReview(t *testing.T) openReview {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "code_review_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(session)
	typedClaude(w, agent)
	awaitClaudeAtItsPrompt(app, session)
	repo := reviewRepo(t)
	document := w.Path("reviews", "calc.md")
	started, err := cli.ReviewStart(document, repo, "HEAD")
	if err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, calcReviewBody)
	if _, err := cli.OpenMarkdown(document, protocol.SessionID(session)); err != nil {
		t.Fatalf("open the review: %v", err)
	}
	awaitDocumentContent(app, document)
	return openReview{w: w, app: app, session: session, agent: agent, document: document, doc: fileAnnotatedDocument(document), repo: repo, head: started.Head}
}

func codeMark(id, kind, text string, docLine, from, to int, quote string) protocol.MarkdownAnnotation {
	return protocol.MarkdownAnnotation{ID: id, Type: kind, Text: protocol.Ptr(text), CreatedAt: int(docLine),
		Anchor: &protocol.MarkdownAnnotationAnchor{StartLine: docLine, EndLine: docLine, Exact: quote,
			File: protocol.Ptr("calc.go"), FileStartLine: protocol.Ptr(from), FileEndLine: protocol.Ptr(to)}}
}

type reviewSend struct {
	onlyIDs []string
	verdict string
	head    string
}

func submitReview(app *testworld.Peer, doc annotatedDocument, session string, send reviewSend) protocol.MarkdownAnnotationsSubmitResultMessage {
	app.T.Helper()
	msg := protocol.MarkdownAnnotationsSubmitMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSubmit, RequestID: uuid.NewString(), DocumentUri: doc.uri, SourceKind: doc.kind,
		Path: doc.path, TargetSessionID: protocol.Ptr(session), OnlyIds: send.onlyIDs,
	}
	if send.verdict != "" {
		msg.Verdict = protocol.Ptr(send.verdict)
	}
	if send.head != "" {
		msg.Head = protocol.Ptr(send.head)
	}
	return testworld.Request(app, msg, protocol.EventMarkdownAnnotationsSubmitResult,
		func(r protocol.MarkdownAnnotationsSubmitResultMessage) bool { return r.RequestID == msg.RequestID })
}

func TestAReviewQuestionReachesTheAgentAtOnceAndItsAnswerReachesTheOpenTile(t *testing.T) {
	r := openCalcReview(t)
	question := codeMark("q1", "question", "Why name it?", 7, 4, 4, "    sum := a + b")
	saveDocumentAnnotations(r.app, r.doc, []protocol.MarkdownAnnotation{question})

	asked := submitReview(r.app, r.doc, r.session, reviewSend{onlyIDs: []string{"q1"}})
	if asked.Status != "delivered" || len(asked.Annotations) != 1 {
		t.Fatalf("asking = %+v, want delivered with the question still in the draft", asked)
	}
	want := "# A question from the user's review\n\nFile: " + r.document + "\n\nOn calc.go:4:\n```\n    sum := a + b\n```\n> Why name it?\n\n" +
		"Answer it with this command. The answer shows under the line in the user's review:\n\n" +
		"attn review answer --doc " + r.document + " q1 -m \"<your answer>\""
	if got := r.agent.Prompted(); got != want {
		t.Errorf("the agent was asked\n%q\nwant\n%q", got, want)
	}

	if err := r.w.Client().ReviewAnswer(r.document, "q1", "So the return reads as a value."); err != nil {
		t.Fatalf("review answer: %v", err)
	}
	pushed := testworld.Await(r.app, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
		return e.Path == r.document && e.AnnotationsGeneration != nil
	})
	draft := getDocumentAnnotations(r.app, r.doc)
	if protocol.Deref(pushed.AnnotationsGeneration) != draft.Generation {
		t.Errorf("the tile was told generation %d, want the draft's %d", protocol.Deref(pushed.AnnotationsGeneration), draft.Generation)
	}
	if len(draft.Annotations) != 1 || protocol.Deref(draft.Annotations[0].Answer) != "So the return reads as a value." {
		t.Errorf("the draft after the answer = %+v, want the question with its answer", draft.Annotations)
	}

	if err := r.w.Client().ReviewAnswer(r.document, "gone", "x"); err == nil || !strings.Contains(err.Error(), "no open question gone") {
		t.Errorf("answering a question the draft does not hold = %v, want an error naming it", err)
	}
}

func TestASentReviewDeliversItsMarksInReadingOrderThenTheVerdictAndBasesTheNextStartOnIt(t *testing.T) {
	r := openCalcReview(t)
	prose := protocol.MarkdownAnnotation{ID: "p1", Type: "comment", Text: protocol.Ptr("Say why."), CreatedAt: 1,
		Anchor: &protocol.MarkdownAnnotationAnchor{BlockID: "b1", StartLine: 3, EndLine: 3, Exact: "keeps its result"}}
	asked := codeMark("q1", "question", "Why name it?", 7, 4, 4, "    sum := a + b")
	asked.Answer = protocol.Ptr("So the return reads as a value.")
	noted := codeMark("n1", "comment", "Return a + b directly.", 7, 5, 6, "    return sum\n}")
	saveDocumentAnnotations(r.app, r.doc, []protocol.MarkdownAnnotation{noted, asked, prose})

	sent := submitReview(r.app, r.doc, r.session, reviewSend{verdict: "request_changes", head: r.head})
	if sent.Status != "delivered" || len(sent.Annotations) != 0 || sent.Error != nil {
		t.Fatalf("the send = %+v, want delivered leaving an empty draft", sent)
	}
	want := "# Review\n\nFile: " + r.document + "\n\n" +
		"I've reviewed the change and have 3 pieces of feedback:\n\n" +
		"## 1. (line 3) Feedback on: \"keeps its result\"\n> Say why.\n\n" +
		"## 2. Question on calc.go:4\n```\n    sum := a + b\n```\n> Why name it?\n\nYou answered: So the return reads as a value.\n\n" +
		"## 3. Note on calc.go:5–6\n```\n    return sum\n}\n```\n> Return a + b directly.\n\n" +
		"---\nVerdict: request changes. Address each note above, then run `attn review start " + r.document + "` and present the change again with `attn open " + r.document + "`."
	if got := r.agent.Prompted(); got != want {
		t.Errorf("the agent got\n%q\nwant\n%q", got, want)
	}

	again, err := r.w.Client().ReviewStart(r.document, r.repo, "")
	if err != nil {
		t.Fatalf("review start after the send: %v", err)
	}
	if protocol.Deref(again.Base) != r.head {
		t.Errorf("the next review start took base %q, want the head the user reviewed %q", protocol.Deref(again.Base), r.head)
	}
}

func TestAVerdictAloneIsAReview(t *testing.T) {
	r := openCalcReview(t)
	if sent := submitReview(r.app, r.doc, r.session, reviewSend{verdict: "approve"}); sent.Status != "delivered" {
		t.Fatalf("an approval with no marks = %+v, want delivered", sent)
	}
	want := "# Review\n\nFile: " + r.document + "\n\nVerdict: approve. The user approves this change. Address the notes above, if any, without presenting it again."
	if got := r.agent.Prompted(); got != want {
		t.Errorf("the agent got\n%q\nwant\n%q", got, want)
	}
}

func TestAReviewThatCannotBeDeliveredTypesNothingAndKeepsTheDraft(t *testing.T) {
	r := openCalcReview(t)
	setSetting(t, r.app, "auto_approve_enabled", "false")
	marks := []protocol.MarkdownAnnotation{codeMark("q1", "question", "Why?", 7, 4, 4, "    sum := a + b")}
	saveDocumentAnnotations(r.app, r.doc, marks)
	if err := r.w.Client().RecordNotification(protocol.TerminalID(r.w.Terminal(r.session)), "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(r.app, r.session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	for _, send := range []reviewSend{{onlyIDs: []string{"q1"}}, {verdict: "approve", head: r.head}} {
		if got := submitReview(r.app, r.doc, r.session, send); got.Status != "skipped_pending_approval" {
			t.Errorf("%+v to a session waiting on approval = %+v, want skipped", send, got)
		}
	}
	if draft := getDocumentAnnotations(r.app, r.doc); len(draft.Annotations) != 1 {
		t.Errorf("the draft after the skipped sends = %+v, want it kept", draft.Annotations)
	}
	if err := r.w.Client().UpdateState(protocol.TerminalID(r.w.Terminal(r.session)), protocol.StateWorking); err != nil {
		t.Fatalf("approve: %v", err)
	}
	testworld.AwaitSession(r.app, r.session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if sent := submitReview(r.app, r.doc, r.session, reviewSend{verdict: "comment"}); sent.Status != "delivered" {
		t.Fatalf("the review after the approval = %+v, want delivered", sent)
	}
	if got := r.agent.Prompted(); !strings.HasPrefix(got, "# Review") {
		t.Errorf("the agent's first prompt was %q, want only the review sent after the approval", got)
	}
}

func TestReviewMarksAreRefusedWithCodeReviewOff(t *testing.T) {
	r := openCalcReview(t)
	saveDocumentAnnotations(r.app, r.doc, []protocol.MarkdownAnnotation{codeMark("q1", "question", "Why?", 7, 4, 4, "    sum := a + b")})
	setSetting(t, r.app, "code_review_enabled", "false")

	for _, send := range []reviewSend{{onlyIDs: []string{"q1"}}, {verdict: "approve"}} {
		if got := submitReview(r.app, r.doc, r.session, send); got.Success || !strings.Contains(protocol.Deref(got.Error), "code_review_enabled") {
			t.Errorf("%+v with code review off = %+v, want refused naming the setting", send, got)
		}
	}
	if err := r.w.Client().ReviewAnswer(r.document, "q1", "x"); err == nil || !strings.Contains(err.Error(), "code_review_enabled") {
		t.Errorf("review answer with code review off = %v, want refused naming the setting", err)
	}
}
