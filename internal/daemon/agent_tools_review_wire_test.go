package daemon_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAnAgentStartsChecksAndAnswersAReviewWithTheReviewTools(t *testing.T) {
	r := openCalcReview(t)
	setSetting(t, r.app, "agent_tools_enabled", "true")
	agent := agentToolsCaller{w: r.w, bearer: terminalCredential(t, r.w, r.app.Terminal(r.session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))

	fresh := r.w.Path("reviews", "fresh.md")
	started := narrowed[protocol.ReviewStartResult](t, shapes.call("review_start", map[string]any{"document": fresh, "repository": r.repo, "base": "HEAD"}).StructuredContent)
	if !started.Created || started.Document != fresh || started.Head == "" || protocol.Deref(started.Base) == "" {
		t.Errorf("review_start = %+v, want a created document over a base", started)
	}
	if body, err := os.ReadFile(fresh); err != nil || !strings.Contains(string(body), "attn: review") {
		t.Errorf("the started document = %q (%v), want review frontmatter", body, err)
	}

	relative := narrowed[protocol.ReviewCheckResult](t, shapes.call("review_check", map[string]any{"document": "../reviews/calc.md"}).StructuredContent)
	absolute := narrowed[protocol.ReviewCheckResult](t, shapes.call("review_check", map[string]any{"document": r.document}).StructuredContent)
	if !reflect.DeepEqual(relative, absolute) {
		t.Errorf("review_check of the path relative to the session folder = %+v, of the absolute path = %+v; want the same answer", relative, absolute)
	}
	broken := narrowed[protocol.ReviewCheckResult](t, shapes.call("review_check", map[string]any{"document": fresh}).StructuredContent)
	if len(broken.Problems) == 0 {
		t.Errorf("review_check of a review with no stops = %+v, want its problems", broken)
	}

	saveDocumentAnnotations(r.app, r.doc, []protocol.MarkdownAnnotation{codeMark("q1", "question", "Why name it?", 7, 4, 4, "    sum := a + b")})
	answered := narrowed[protocol.ReviewAnswerResult](t, shapes.call("review_answer", map[string]any{"document": "../reviews/calc.md", "question": "q1", "text": "So the return reads as a value."}).StructuredContent)
	if answered.Question != "q1" || answered.Document != r.document {
		t.Errorf("review_answer = %+v, want q1 on %s", answered, r.document)
	}
	if draft := getDocumentAnnotations(r.app, r.doc); len(draft.Annotations) != 1 || protocol.Deref(draft.Annotations[0].Answer) != "So the return reads as a value." {
		t.Errorf("the draft after review_answer = %+v, want the question with its answer", draft.Annotations)
	}
	gone := agent.mustCall(t, "review_answer", map[string]any{"document": r.document, "question": "gone", "text": "x"})
	if text := resultText(gone); !gone.IsError || !strings.HasPrefix(text, "no_question: ") || !strings.Contains(text, "\nFix: ") {
		t.Errorf("review_answer of a question the draft does not hold = %q, want the no_question refusal with a fix", text)
	}
	shapes.finish("review_")
}
