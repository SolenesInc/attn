package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/harness"
)

func (d *Daemon) trustCrewHome(member crew.Member, agent, home string) {
	if strings.TrimSpace(member.CWD) != "" {
		return
	}
	trust, ok := harness.As[harness.Trust](d.harnessOf(agent))
	if !ok {
		return
	}
	if err := trust.TrustFolder(home); err != nil {
		d.logf("crew: %s's harness will ask whether to trust its home %s: %v", member.Name, home, err)
	}
}
