package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentSetsUpAndChecksCodeNavigationWithTheNavTools(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	setSetting(t, app, "code_review_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))
	repo := navigatedRepo(t)
	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), w.Path("installed"))

	linked := narrowed[protocol.CodeNavLinkResult](t, shapes.call("nav_link", map[string]any{"program": fake}).StructuredContent)
	if protocol.Deref(linked.Target) != fake || filepath.Base(linked.Path) != filepath.Base(fake) {
		t.Errorf("nav_link = %+v, want %s linked under its own name", linked, fake)
	}
	status := narrowed[protocol.CodeNavStatusResult](t, shapes.call("nav_status", map[string]any{"repository": repo}).StructuredContent)
	if protocol.Deref(status.Origin) == "" || len(status.Languages) == 0 {
		t.Errorf("nav_status = %+v, want the repository's origin and its languages", status)
	}

	checked := narrowed[protocol.CodeNavCheckResult](t, shapes.call("nav_check", map[string]any{"at": "use.go:4", "repository": repo}).StructuredContent)
	if protocol.Deref(checked.Name) != "Add" || checked.Location == nil || checked.Location.Path != "calc.go" || protocol.Deref(checked.Starting) {
		t.Errorf("nav_check = %+v, want Add resolved to calc.go by a server that answered within the bound", checked)
	}

	set := narrowed[protocol.CodeNavDefineResult](t, shapes.call("nav_set", map[string]any{"language": "zig", "definition": `{"command": ["zls"], "extensions": [".zig"]}`, "repository": repo}).StructuredContent)
	if set.Key != "code_navigation_example.test/calc_zig" || protocol.Deref(set.Definition) == "" {
		t.Errorf("nav_set = %+v, want zig defined for the repository's origin", set)
	}
	removed := narrowed[protocol.CodeNavDefineResult](t, shapes.call("nav_rm", map[string]any{"language": "zig", "repository": repo}).StructuredContent)
	if removed.Key != set.Key || removed.Definition != nil {
		t.Errorf("nav_rm = %+v, want %s removed", removed, set.Key)
	}
	unlinked := narrowed[protocol.CodeNavLinkResult](t, shapes.call("nav_unlink", map[string]any{"name": filepath.Base(fake)}).StructuredContent)
	if unlinked.Path != linked.Path {
		t.Errorf("nav_unlink = %+v, want %s removed", unlinked, linked.Path)
	}

	gone := agent.mustCall(t, "nav_unlink", map[string]any{"name": filepath.Base(fake)})
	if text := resultText(gone); !gone.IsError || !strings.Contains(text, "nothing is linked as") {
		t.Errorf("nav_unlink of a name no longer linked = %q, want the refusal naming it", text)
	}
	shapes.finish("nav_")
}
