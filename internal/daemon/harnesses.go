package daemon

import (
	"fmt"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
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
		Executable: harness.Executable{Default: driver.DefaultExecutable(), Env: driver.ExecutableEnvVar()},
		Signals:    hookSignals(caps.HasHooks),
	}
}

func hookSignals(hooks bool) harness.Signals {
	if !hooks {
		return harness.Signals{}
	}
	return harness.Signals{TurnStarts: harness.TurnStartsReported, ReportsTools: true}
}

func (d *Daemon) signalsOf(agent protocol.SessionAgent) harness.Signals {
	if agent == protocol.SessionAgentShell {
		return sessionstate.Shell
	}
	if h, err := d.harness(string(agent)); err == nil {
		return h.Facts().Signals
	}
	return sessionstate.Unregistered
}

func (d *Daemon) harnessOwnsState(sessionID protocol.SessionID) bool {
	if d.store.GetAgentDriverRun(sessionID).RunID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && d.signalsOf(session.Agent).StateAuthority == harness.StateByHarness
}

func (d *Daemon) executableHealth(facts harness.Facts, offered bool) harness.Health {
	checking := harness.Health{Installation: harness.Unknown, Offered: offered, Executable: facts.Executable.Default, Reason: "checking the login environment for " + facts.DisplayName}
	if d.capturingLoginEnv() {
		return checking
	}
	executable, err := d.machineExecutableOf(facts, "")
	if err != nil {
		if d.loginEnvFailed() {
			return checking
		}
		return harness.Health{Installation: harness.Missing, Offered: offered, Executable: executable, Reason: err.Error()}
	}
	return harness.Health{Installation: harness.Installed, Offered: offered, Executable: executable}
}
