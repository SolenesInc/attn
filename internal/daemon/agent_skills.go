package daemon

import (
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
)

func (d *Daemon) ensureAvailableAgentSkills() {
	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		if driver == nil || !d.agentOffered(name) || !d.isAgentExecutableAvailable(driver, "") {
			continue
		}
		if synced, err := agentdriver.EnsureAttnSkillInstalled(name); err != nil {
			d.logf("failed to ensure %s attn skill: %v", name, err)
		} else if !synced {
			d.logf("skipping user-global %s attn skill sync for instance %q", name, config.InstanceLabel())
		}
	}
	d.ensureEnabledWorkflowSkills()
}

func (d *Daemon) ensureEnabledWorkflowSkills() {
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil || !cfg.WorkflowSkillEnabled {
		return
	}
	var harnesses []string
	for _, harness := range d.delegationHarnesses() {
		if harness.Available {
			harnesses = append(harnesses, harness.ID)
		}
	}
	if _, synced, err := agentdriver.EnsureWorkflowSkillsInstalled(harnesses); err != nil {
		d.logf("failed to ensure attn-workflow skill: %v", err)
	} else if !synced {
		d.logf("skipping user-global attn-workflow skill sync for instance %q", config.InstanceLabel())
	}
}
