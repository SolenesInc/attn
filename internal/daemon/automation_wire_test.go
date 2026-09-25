package daemon_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAutomationReapplyEditsOnlyOnChangeAndTogglesAreIdempotent(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}

	applied := applyAutomation(t, cli, manualAutomation(w, "Check locally."))
	if applied.Revision != 1 || !applied.Enabled {
		t.Fatalf("first apply = %+v, want revision 1, enabled", applied)
	}
	if unchanged := applyAutomation(t, cli, manualAutomation(w, "Check locally.")); unchanged.Revision != 1 || !unchanged.Enabled {
		t.Errorf("re-applying the same definition = %+v, want it untouched", unchanged)
	}
	if edited := applyAutomation(t, cli, manualAutomation(w, "Check locally, twice.")); edited.Revision != 2 {
		t.Errorf("an edited definition is at revision %d, want 2", edited.Revision)
	}

	disabled := setAutomationEnabled(t, cli, "manual-check", false)
	if disabled.Enabled {
		t.Fatalf("disable = %+v", disabled)
	}
	if again := setAutomationEnabled(t, cli, "manual-check", false); again.Enabled || again.UpdatedAt != disabled.UpdatedAt {
		t.Errorf("a repeated disable = %+v, want a no-op on %+v", again, disabled)
	}
	if reapplied := applyAutomation(t, cli, manualAutomation(w, "Check locally, twice.")); reapplied.Enabled {
		t.Error("re-applying a disabled automation enabled it")
	}
	if enabled := setAutomationEnabled(t, cli, "manual-check", true); !enabled.Enabled || enabled.Revision != 2 {
		t.Errorf("enable = %+v, want enabled at the same revision", enabled)
	}
	if _, err := cli.AutomationSetEnabled("does-not-exist", true); err == nil {
		t.Error("enabling an unknown automation was accepted")
	}
}

func manualAutomation(w *world, prompt string) string {
	return fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: manual-check
name: Manual check
trigger: {type: manual}
prompt: %s
launch: {driver: codex}
location: {type: directory, path: %q}
`, prompt, w.Path("check"))
}

func applyAutomation(t *testing.T, cli *client.Client, spec string) protocol.AutomationDefinitionSummary {
	t.Helper()
	applied, err := cli.AutomationApply(spec)
	if err != nil {
		t.Fatalf("apply automation: %v", err)
	}
	return *applied.Definition
}

func setAutomationEnabled(t *testing.T, cli *client.Client, id string, enabled bool) protocol.AutomationDefinitionSummary {
	t.Helper()
	result, err := cli.AutomationSetEnabled(id, enabled)
	if err != nil {
		t.Fatalf("set %s enabled=%t: %v", id, enabled, err)
	}
	return *result.Definition
}
