package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
)

func TestTheSkillAndGuideToolsPrintTheMapReferencesAndPages(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}

	for _, tc := range []struct {
		tool string
		args map[string]any
		want []string
	}{
		{"skill", map[string]any{}, []string{"## What attn can do", "skill({reference: \"<name>\"})"}},
		{"skill", map[string]any{"list": true}, []string{"garden\n", "delegation\n"}},
		{"skill", map[string]any{"reference": "garden"}, []string{"# The garden"}},
		{"skill", map[string]any{"reference": "garden.md"}, []string{"# The garden"}},
		{"guide", map[string]any{}, []string{"attn guide <topic>", "Tutorials", "getting-started", "using-sigma"}},
		{"guide", map[string]any{"topic": "queue"}, []string{"# Queue flow", "```text"}},
	} {
		result := agent.mustCall(t, tc.tool, tc.args)
		text := resultText(result)
		if result.IsError {
			t.Errorf("%s %v refused: %s", tc.tool, tc.args, text)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s %v answered without %q:\n%s", tc.tool, tc.args, want, text)
			}
		}
	}

	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"skill", map[string]any{"reference": "workflow"}, "no_such_reference: no bundled reference \"workflow\"; bundled references: "},
		{"skill", map[string]any{"reference": "garden", "list": true}, "list_and_reference: list and reference are mutually exclusive; pass one\nFix: Pass reference or list, not both."},
		{"guide", map[string]any{"topic": "nope"}, "no_such_topic: no topic \"nope\"; topics: "},
	} {
		refused := agent.mustCall(t, tc.tool, tc.args)
		if text := resultText(refused); !refused.IsError || !strings.HasPrefix(text, tc.want) || !strings.Contains(text, "\nFix: ") {
			t.Errorf("%s %v answered %q, want a refusal starting %q with a fix", tc.tool, tc.args, text, tc.want)
		}
	}
}
