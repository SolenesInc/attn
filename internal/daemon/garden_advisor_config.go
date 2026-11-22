package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/setting"
)

const gardenAdvisorDefaultAgent = "codex"

type gardenAdvisorConfig struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

func (d *Daemon) parseGardenAdvisorConfig(raw string) (gardenAdvisorConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gardenAdvisorConfig{Agent: gardenAdvisorDefaultAgent}, nil
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

	if _, err := d.oneShot(config.Agent); err != nil {
		return gardenAdvisorConfig{}, fmt.Errorf("garden advisor agent is not supported: %s", config.Agent)
	}
	return config, nil
}

func (d *Daemon) validateGardenAdvisorSetting(raw string) error {
	config, err := d.parseGardenAdvisorConfig(raw)
	if err != nil {
		return err
	}
	if _, err := d.machineAgentExecutable(config.Agent, ""); err != nil {
		return fmt.Errorf("garden advisor executable for %s was not found: %w", config.Agent, err)
	}
	return nil
}

func (d *Daemon) gardenAdvisorConfig(ctx context.Context) (gardenAdvisorConfig, error) {
	if d.store == nil {
		return gardenAdvisorConfig{}, errors.New("garden advisor settings unavailable")
	}
	config, err := d.parseGardenAdvisorConfig(d.setting(setting.GardenAdvisor))
	if err != nil {
		return gardenAdvisorConfig{}, err
	}
	advisor := d.factsOrNone(config.Agent).Advisor
	fallback := advisor.Model
	if config.Effort == "" {
		config.Effort = advisor.Effort
	}
	if !headless.Enabled() {
		if config.Model == "" {
			config.Model = fallback
		}
		return config, nil
	}
	config.Model = d.resolveTierModel(ctx, config.Agent, modeltiers.Light, config.Model, fallback)
	return config, nil
}
