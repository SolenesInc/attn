package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

const sessionActivityKind = "session_activity"

// Measured (receipts in docs/plans/2026-08-07-session-activity.md): Codex/Luna 4.8s p50
// at $0.0027 a run, Claude/Haiku 11.7s at $0.011-0.017; effort measured inert on Claude.
const (
	activityClaudeDefaultModel = "claude-haiku-4-5"
	activityCodexDefaultModel  = "gpt-5.6-luna"
	activityCodexDefaultEffort = "low"
)

const (
	defaultActivityWatchingSeconds = 120
	defaultActivityPresentSeconds  = 300
	activityIntervalMinSeconds     = 30
	activityIntervalMaxSeconds     = 3600
)

// UNMEASURED — a guess, safe because `away` self-heals on the next input.
const (
	defaultActivityPresenceIdleSeconds = 90
	activityPresenceIdleMinSeconds     = 10
	activityPresenceIdleMaxSeconds     = 3600
)

type activityConfig struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

var errActivityAgentUnset = errors.New("session activity has no agent selected: choose claude or codex")

func parseActivityConfig(raw string) (activityConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return activityConfig{}, errActivityAgentUnset
	}

	var config activityConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return activityConfig{}, fmt.Errorf("invalid session activity configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return activityConfig{}, fmt.Errorf("invalid session activity configuration: %w", err)
	}

	config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
	config.Model = strings.TrimSpace(config.Model)
	config.Effort = strings.TrimSpace(strings.ToLower(config.Effort))
	if config.Agent == "" {
		return activityConfig{}, errActivityAgentUnset
	}

	if config.Model == "" {
		switch config.Agent {
		case "claude":
			config.Model = activityClaudeDefaultModel
		case "codex":
			config.Model = activityCodexDefaultModel
		default:
			return activityConfig{}, fmt.Errorf("session activity requires a model for agent %s", config.Agent)
		}
	}
	if config.Effort == "" && config.Agent == "codex" {
		config.Effort = activityCodexDefaultEffort
	}

	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return activityConfig{}, fmt.Errorf("session activity agent is not installed: %s", config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return activityConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return activityConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	return config, nil
}

func (d *Daemon) validateActivitySetting(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	config, err := parseActivityConfig(raw)
	if err != nil {
		return err
	}
	driver := agentdriver.Get(config.Agent)
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executable := driver.ResolveExecutable(configured)
	if _, err := exec.LookPath(executable); err != nil {
		return fmt.Errorf("session activity executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

type activityIntervals struct {
	Watching int `json:"watching"`
	Present  int `json:"present"`
}

func parseActivityIntervals(raw string) (activityIntervals, error) {
	intervals := activityIntervals{
		Watching: defaultActivityWatchingSeconds,
		Present:  defaultActivityPresentSeconds,
	}
	if strings.TrimSpace(raw) == "" {
		return intervals, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intervals); err != nil {
		return activityIntervals{}, fmt.Errorf("invalid session activity intervals: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return activityIntervals{}, fmt.Errorf("invalid session activity intervals: %w", err)
	}
	intervals.Watching = clampInterval(intervals.Watching, defaultActivityWatchingSeconds)
	intervals.Present = clampInterval(intervals.Present, defaultActivityPresentSeconds)
	return intervals, nil
}

func clampInterval(seconds, fallback int) int {
	if seconds <= 0 {
		return fallback
	}
	if seconds < activityIntervalMinSeconds {
		return activityIntervalMinSeconds
	}
	if seconds > activityIntervalMaxSeconds {
		return activityIntervalMaxSeconds
	}
	return seconds
}

func (d *Daemon) activityEnabled() bool {
	if d.store == nil {
		return false
	}
	return parseBooleanSetting(d.store.GetSetting(SettingActivityEnabled))
}

func (d *Daemon) activityConfigured() (activityConfig, error) {
	if d.store == nil {
		return activityConfig{}, errors.New("session activity settings unavailable")
	}
	return parseActivityConfig(d.store.GetSetting(SettingActivityConfig))
}

// `away` returns zero, which every caller must read as "generate nothing", never
// "generate now".
func (d *Daemon) activityInterval(tier PresenceTier) time.Duration {
	if tier == PresenceAway {
		return 0
	}
	raw := ""
	if d.store != nil {
		raw = d.store.GetSetting(SettingActivityIntervals)
	}
	intervals, err := parseActivityIntervals(raw)
	if err != nil {
		// Falls back to defaults, never zero: zero silently stops the feature.
		d.logf("activity: intervals setting is invalid (%v); using defaults", err)
		intervals = activityIntervals{
			Watching: defaultActivityWatchingSeconds,
			Present:  defaultActivityPresentSeconds,
		}
	}
	if tier == PresenceWatching {
		return time.Duration(intervals.Watching) * time.Second
	}
	return time.Duration(intervals.Present) * time.Second
}

func (d *Daemon) presenceIdleLimit() time.Duration {
	seconds := defaultActivityPresenceIdleSeconds
	if d.store != nil {
		seconds = resolveBoundedIntSetting(
			d.store.GetSetting(SettingActivityPresenceIdleSeconds),
			defaultActivityPresenceIdleSeconds,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	}
	return time.Duration(seconds) * time.Second
}

func (d *Daemon) resolveActivityExecutable(config activityConfig) (agentdriver.HeadlessTaskProvider, string, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return nil, "", fmt.Errorf("session activity agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return nil, "", fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	executablePath, err := exec.LookPath(driver.ResolveExecutable(configured))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	return provider, executablePath, nil
}
