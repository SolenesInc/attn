package daemon

import (
	"github.com/victorarias/attn/internal/attnskill"
	"github.com/victorarias/attn/internal/config"
)

func (d *Daemon) ensureAvailableAgentSkills() {
	if synced, kept, err := attnskill.ReplaceRetiredAttnSkillWithPointer(); err != nil {
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
	var harnesses []string
	for _, harness := range d.delegationHarnesses() {
		if harness.Available() {
			harnesses = append(harnesses, harness.ID)
		}
	}
	d.ensureEnabledWorkflowSkillsFor(harnesses)
}

func (d *Daemon) ensureEnabledWorkflowSkillsFor(harnesses []string) {
	d.ensureEnabledWorkflowSkillsIn(d.workflowSkillsDirs(harnesses))
}

func (d *Daemon) ensureEnabledWorkflowSkillsIn(skillsDirs func(homeDir string) ([]string, error)) {
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil || !cfg.WorkflowSkillEnabled {
		return
	}
	if _, synced, err := attnskill.EnsureWorkflowSkillsInstalled(skillsDirs); err != nil {
		d.logf("failed to ensure attn-workflow skill: %v", err)
	} else if !synced {
		d.logf("skipping user-global attn-workflow skill sync for instance %q", config.InstanceLabel())
	}
}
