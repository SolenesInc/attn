package daemon

import (
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
)

var harnessSources []func(*Daemon) []harness.Harness

func (d *Daemon) harnesses() []harness.Harness {
	var all []harness.Harness
	for _, source := range harnessSources {
		all = append(all, source(d)...)
	}
	return all
}

func (d *Daemon) harness(id string) (harness.Harness, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, h := range d.harnesses() {
		if h.Facts().ID == id {
			return h, nil
		}
	}
	return nil, fmt.Errorf("agent %q is not available", id)
}

func (d *Daemon) knowsHarness(id string) bool {
	_, err := d.harness(id)
	return err == nil
}

func (d *Daemon) factsOrNone(id string) harness.Facts {
	if h, err := d.harness(id); err == nil {
		return h.Facts()
	}
	return harness.Facts{ID: id}
}

func (d *Daemon) offered(id string) bool {
	h, err := d.harness(id)
	return err == nil && h.Health(d.life.Context()).Offered
}

func builtinFacts(driver agentdriver.Driver) harness.Facts {
	caps := agentdriver.EffectiveCapabilities(driver)
	_, declaresServer := agentdriver.DeclaredServer(driver)
	return harness.Facts{
		ID:          driver.Name(),
		DisplayName: driver.DisplayName(),
		Options: harness.Options{
			Resume:             caps.HasResume,
			Yolo:               caps.HasYolo,
			AutoApprove:        caps.HasAutoApprove,
			InitialPrompt:      caps.HasInitialPrompt,
			LaunchInstructions: caps.HasLaunchInstructions,
			ModelPin:           caps.HasModelPin,
			EffortPin:          caps.HasEffortPin,
			ContextCap:         caps.HasContextWindowCap,
			Picker:             true,
			Provider:           declaresServer,
		},
	}
}

func (d *Daemon) executableHealth(driver agentdriver.Driver, offered bool) harness.Health {
	checking := harness.Health{Installation: harness.Unknown, Offered: offered, Executable: driver.DefaultExecutable(), Reason: "checking the login environment for " + driver.DisplayName()}
	if d.capturingLoginEnv() {
		return checking
	}
	executable, err := d.machineAgentExecutable(driver, "")
	if err != nil {
		if d.loginEnvFailed() {
			return checking
		}
		return harness.Health{Installation: harness.Missing, Offered: offered, Executable: executable, Reason: err.Error()}
	}
	return harness.Health{Installation: harness.Installed, Offered: offered, Executable: executable}
}
