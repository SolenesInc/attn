package daemon

import (
	"testing"
)

func TestActivityConfigFillsInTheAgentsDefaults(t *testing.T) {
	claude, err := parseActivityConfig(`{"agent":"claude"}`)
	if err != nil {
		t.Fatalf("claude: %v", err)
	}
	if claude.Model != activityClaudeDefaultModel {
		t.Errorf("claude model = %q, want %q", claude.Model, activityClaudeDefaultModel)
	}
	if claude.Effort != "" {
		t.Errorf("claude effort = %q, want unset", claude.Effort)
	}

	codex, err := parseActivityConfig(`{"agent":"codex"}`)
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if codex.Model != activityCodexDefaultModel {
		t.Errorf("codex model = %q, want %q", codex.Model, activityCodexDefaultModel)
	}
	if codex.Effort != activityCodexDefaultEffort {
		t.Errorf("codex effort = %q, want %q", codex.Effort, activityCodexDefaultEffort)
	}
}

func TestActivityConfigKeepsAnExplicitChoice(t *testing.T) {
	config, err := parseActivityConfig(`{"agent":"codex","model":"gpt-5.6","effort":"medium"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if config.Model != "gpt-5.6" || config.Effort != "medium" {
		t.Errorf("config = %+v, want the explicit model and effort", config)
	}
}

func TestActivityIntervalsDefaultAndClamp(t *testing.T) {
	defaults, err := parseActivityIntervals("")
	if err != nil {
		t.Fatalf("blank: %v", err)
	}
	if defaults.Watching != defaultActivityWatchingSeconds || defaults.Present != defaultActivityPresentSeconds {
		t.Errorf("defaults = %+v", defaults)
	}

	clamped, err := parseActivityIntervals(`{"watching":1,"present":99999}`)
	if err != nil {
		t.Fatalf("out of range: %v", err)
	}
	if clamped.Watching != activityIntervalMinSeconds {
		t.Errorf("watching = %d, want clamped to %d", clamped.Watching, activityIntervalMinSeconds)
	}
	if clamped.Present != activityIntervalMaxSeconds {
		t.Errorf("present = %d, want clamped to %d", clamped.Present, activityIntervalMaxSeconds)
	}

	if _, err := parseActivityIntervals(`{"watching":120,"away":0}`); err == nil {
		t.Error("an `away` interval was accepted; away is a stop, not a rate")
	}
}

func TestActivityIntervalIsZeroWhenAway(t *testing.T) {
	d := &Daemon{}
	if got := d.activityInterval(PresenceAway); got != 0 {
		t.Errorf("away interval = %v, want zero", got)
	}
	if got := d.activityInterval(PresenceWatching); got == 0 {
		t.Error("watching interval is zero, which would read as away")
	}
	if d.activityInterval(PresenceWatching) >= d.activityInterval(PresencePresent) {
		t.Error("watching must refresh faster than present")
	}
}
