package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

const (
	gardenAdvisorDefaultAgent        = "codex"
	gardenAdvisorCodexDefaultModel   = "gpt-5.6-luna"
	gardenAdvisorCodexDefaultEffort  = "xhigh"
	gardenAdvisorClaudeDefaultModel  = "sonnet"
	gardenAdvisorClaudeDefaultEffort = "medium"
	gardenAdvisorCopilotDefaultModel = "claude-sonnet-4.6"
)

type gardenAdvisorConfig struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

func defaultGardenAdvisorConfig(agent string) (gardenAdvisorConfig, error) {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case "codex":
		return gardenAdvisorConfig{
			Agent:  "codex",
			Model:  gardenAdvisorCodexDefaultModel,
			Effort: gardenAdvisorCodexDefaultEffort,
		}, nil
	case "claude":
		return gardenAdvisorConfig{
			Agent:  "claude",
			Model:  gardenAdvisorClaudeDefaultModel,
			Effort: gardenAdvisorClaudeDefaultEffort,
		}, nil
	case "copilot":
		return gardenAdvisorConfig{
			Agent: "copilot",
			Model: gardenAdvisorCopilotDefaultModel,
		}, nil
	default:
		return gardenAdvisorConfig{}, fmt.Errorf("garden advisor agent is not supported: %s", agent)
	}
}

func parseGardenAdvisorConfig(raw string) (gardenAdvisorConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultGardenAdvisorConfig(gardenAdvisorDefaultAgent)
	}

	var config gardenAdvisorConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return gardenAdvisorConfig{}, fmt.Errorf("invalid Garden advisor configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return gardenAdvisorConfig{}, fmt.Errorf("invalid Garden advisor configuration: %w", err)
	}

	config.Agent = strings.TrimSpace(strings.ToLower(config.Agent))
	config.Model = strings.TrimSpace(config.Model)
	config.Effort = strings.TrimSpace(strings.ToLower(config.Effort))
	if config.Agent == "" {
		return gardenAdvisorConfig{}, errors.New("garden advisor requires an agent")
	}

	defaults, err := defaultGardenAdvisorConfig(config.Agent)
	if err != nil {
		return gardenAdvisorConfig{}, err
	}
	if config.Model == "" {
		config.Model = defaults.Model
	}
	if config.Effort == "" {
		config.Effort = defaults.Effort
	}
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return gardenAdvisorConfig{}, fmt.Errorf("garden advisor agent is not installed: %s", config.Agent)
	}
	if _, ok := driver.(agentdriver.HeadlessTaskProvider); !ok {
		return gardenAdvisorConfig{}, fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	if available, reason := agentdriver.HeadlessTaskAvailability(driver); !available {
		return gardenAdvisorConfig{}, fmt.Errorf("agent %s cannot run headless tasks: %s", config.Agent, reason)
	}
	return config, nil
}

func (d *Daemon) validateGardenAdvisorSetting(raw string) error {
	config, err := parseGardenAdvisorConfig(raw)
	if err != nil {
		return err
	}
	driver := agentdriver.Get(config.Agent)
	configured := ""
	if d.store != nil {
		configured = d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	}
	if _, err := exec.LookPath(driver.ResolveExecutable(configured)); err != nil {
		return fmt.Errorf("garden advisor executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

func (d *Daemon) gardenAdvisorConfig() (gardenAdvisorConfig, error) {
	if d.store == nil {
		return gardenAdvisorConfig{}, errors.New("garden advisor settings unavailable")
	}
	return parseGardenAdvisorConfig(d.store.GetSetting(SettingGardenAdvisor))
}

func (d *Daemon) resolveGardenAdvisor(
	config gardenAdvisorConfig,
) (agentdriver.HeadlessTaskProvider, string, error) {
	driver := agentdriver.Get(config.Agent)
	if driver == nil {
		return nil, "", fmt.Errorf("garden advisor agent not found: %s", config.Agent)
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return nil, "", fmt.Errorf("agent %s does not support headless tasks", config.Agent)
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey(config.Agent))
	executable, err := exec.LookPath(driver.ResolveExecutable(configured))
	if err != nil {
		return nil, "", fmt.Errorf("resolve %s executable: %w", config.Agent, err)
	}
	return provider, executable, nil
}
