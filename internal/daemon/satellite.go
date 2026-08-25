package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) resolveSpawnParent(spawnedFrom, workspaceID string, isShell bool) string {
	if d == nil || d.store == nil || !isShell {
		return ""
	}
	baseID := strings.TrimSpace(spawnedFrom)
	if baseID == "" {
		return ""
	}
	base := d.store.Get(baseID)
	if base == nil {
		return ""
	}
	parentID := base.ID
	if string(base.Agent) == protocol.AgentShellValue {
		parentID = strings.TrimSpace(protocol.Deref(base.ParentSessionID))
		if parentID == "" {
			return ""
		}
	}
	parent := d.store.Get(parentID)
	if parent == nil {
		return ""
	}
	if strings.TrimSpace(parent.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return ""
	}
	return parent.ID
}
