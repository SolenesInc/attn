package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnAgentOpensADocumentOrASeedWithTheOpenTool(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))
	document := filepath.Join(w.Path("shop"), "notes", "plan.md")
	if err := os.MkdirAll(filepath.Dir(document), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("# The plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, given := range []string{"notes/plan.md", document, "notes/plan.md"} {
		opened := narrowed[protocol.OpenResult](t, shapes.call("open", map[string]any{"path": given}).StructuredContent)
		if opened.Opened != document || len(opened.Problems) != 0 {
			t.Errorf("open of %q = %+v, want %s opened with no problems", given, opened, document)
		}
	}
	awaitDocumentContent(app, document)

	setSetting(t, app, "code_review_enabled", "true")
	review := filepath.Join(w.Path("shop"), "review.md")
	if err := os.WriteFile(review, []byte("---\nattn: review\n---\n# Broken\n\n### nowhere.go:1-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warned := narrowed[protocol.OpenResult](t, shapes.call("open", map[string]any{"path": "review.md"}).StructuredContent)
	checked := narrowed[protocol.ReviewCheckResult](t, agent.mustCall(t, "review_check", map[string]any{"document": review}).StructuredContent)
	if warned.Opened != review || len(warned.Problems) == 0 || len(warned.Problems) != len(checked.Problems) {
		t.Errorf("open of a review document with problems = %+v, want the problems review_check lists (%+v)", warned, checked.Problems)
	}

	planted := narrowed[protocol.SeedPlantResult](t, agent.mustCall(t, "seed_plant", map[string]any{"title": "Show me"}).StructuredContent)
	if seed := narrowed[protocol.OpenResult](t, shapes.call("open", map[string]any{"seed": planted.ID}).StructuredContent); seed.Opened != planted.ID {
		t.Errorf("open of seed %s = %+v, want it opened", planted.ID, seed)
	}

	missing := agent.mustCall(t, "open", map[string]any{})
	if text := resultText(missing); !missing.IsError || !strings.HasPrefix(text, "no_target: ") || !strings.Contains(text, "\nFix: Pass path or seed.") {
		t.Errorf("open with neither path nor seed = %q, want the no_target refusal naming both args", text)
	}
	shapes.finish("open")
}
