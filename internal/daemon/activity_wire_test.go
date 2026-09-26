package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestActivityStaysOffUntilTheUserNamesAnAgentItCanRun(t *testing.T) {
	const unsetAgent = "no agent selected"
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "activity.enabled", "true")
	namesTheUnsetAgent := func(after string) {
		t.Helper()
		status, err := cli.ActivityStatus()
		if err != nil {
			t.Fatalf("activity status after %s: %v", after, err)
		}
		if !status.Enabled || !strings.Contains(protocol.Deref(status.Error), unsetAgent) {
			t.Errorf("after %s activity status = enabled %t, error %q; want it enabled and naming the missing agent", after, status.Enabled, protocol.Deref(status.Error))
		}
	}

	for _, cleared := range []string{"", "   "} {
		setSetting(t, app, "activity.config", cleared)
		namesTheUnsetAgent("clearing the config to " + `"` + cleared + `"`)
	}

	for _, refused := range []struct{ raw, names string }{
		{`{}`, unsetAgent},
		{`{"model":"claude-haiku-4-5"}`, unsetAgent},
		{`{"agent":"  "}`, unsetAgent},
		{`{"agent":"nonesuch","model":"m"}`, "not installed: nonesuch"},
		{`{"agent":"claude","mdoel":"typo"}`, `unknown field "mdoel"`},
		{`{"agent":"claude"} and more`, "invalid session activity configuration"},
		{`claude`, "invalid session activity configuration"},
	} {
		result := gardenAdvisorSetSetting(app, "activity.config", refused.raw)
		if protocol.Deref(result.Success) || !strings.Contains(protocol.Deref(result.Error), refused.names) {
			t.Errorf("setting the activity config to %s = success %t, error %q; want a refusal naming %q", refused.raw, protocol.Deref(result.Success), protocol.Deref(result.Error), refused.names)
		}
		namesTheUnsetAgent("the refused " + refused.raw)
	}
}
