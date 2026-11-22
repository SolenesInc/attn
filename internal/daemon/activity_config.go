package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/setting"
)

const sessionActivityKind = "session_activity"

const (
	activityClaudeDefaultModel = "claude-haiku-4-5"
	activityCodexDefaultModel  = "gpt-5.6-luna"
)

const (
	defaultActivityWatchingSeconds = 120
	defaultActivityPresentSeconds  = 300
	activityIntervalMinSeconds     = 30
	activityIntervalMaxSeconds     = 3600
)

const (
	defaultActivityPresenceIdleSeconds = 90
	activityPresenceIdleMinSeconds     = 10
	activityPresenceIdleMaxSeconds     = 3600
)

type activityConfig struct {
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	fallback string
}

var errActivityAgentUnset = errors.New("session activity has no agent selected: choose claude or codex")

func (d *Daemon) parseActivityConfig(raw string) (activityConfig, error) {
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

	config.fallback = d.lightFallback(config.Agent, activityFallbackModel(config.Agent))
	if config.Model == "" && config.fallback == "" {
		return activityConfig{}, fmt.Errorf("session activity requires a model for agent %s", config.Agent)
	}
	if config.Effort == "" {
		config.Effort = d.factsOrNone(config.Agent).Tiers[modeltiers.Light].Effort
	}

	if !d.knowsHarness(config.Agent) {
		return activityConfig{}, fmt.Errorf("session activity agent is not installed: %s", config.Agent)
	}
	if _, err := d.oneShot(config.Agent); err != nil {
		return activityConfig{}, err
	}
	return config, nil
}

func activityFallbackModel(agent string) string {
	switch agent {
	case "claude":
		return activityClaudeDefaultModel
	case "codex":
		return activityCodexDefaultModel
	}
	return ""
}

func (d *Daemon) validateActivitySetting(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	config, err := d.parseActivityConfig(raw)
	if err != nil {
		return err
	}
	if _, err := d.machineAgentExecutable(config.Agent, ""); err != nil {
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
	return d.on(setting.ActivityEnabled)
}

func (d *Daemon) activityConfigured() (activityConfig, error) {
	if d.store == nil {
		return activityConfig{}, errors.New("session activity settings unavailable")
	}
	return d.parseActivityConfig(d.setting(setting.ActivityConfig))
}

func (d *Daemon) activityInterval(tier PresenceTier) time.Duration {
	if tier == PresenceAway {
		return 0
	}
	raw := ""
	if d.store != nil {
		raw = d.setting(setting.ActivityIntervals)
	}
	intervals, err := parseActivityIntervals(raw)
	if err != nil {
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
			d.setting(setting.ActivityPresenceIdleSeconds),
			defaultActivityPresenceIdleSeconds,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	}
	return time.Duration(seconds) * time.Second
}
