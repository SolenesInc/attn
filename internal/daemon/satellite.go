package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) resolveSpawnParent(spawnedFrom string, profile profiles.Profile, placement *launchPlacement, isShell bool) string {
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
	if placement == nil {
		return ""
	}
	parentPlacement, placed, err := d.store.SessionPlacement(parent.ID)
	if err != nil || !placed {
		return ""
	}
	targetDesktopID := placement.desktopID
	if targetDesktopID == "" {
		targetDesktopID = profile.CurrentDesktopID
	}
	if parentPlacement.DesktopID != targetDesktopID {
		return ""
	}
	return parent.ID
}
