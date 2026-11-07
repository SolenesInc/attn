package daemon

import (
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
)

func (d *Daemon) ensureAvailableAgentSkills() {
	if synced, kept, err := agentdriver.ReplaceRetiredAttnSkillWithPointer(); err != nil {
		d.logf("failed to replace the retired attn skill with its pointer: %v", err)
	} else if !synced {
		d.logf("skipping the retired attn skill's pointer for instance %q", config.InstanceLabel())
	} else {
		for _, skillDir := range kept {
			d.logf("kept the skill at %s: it holds files attn did not install", skillDir)
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
