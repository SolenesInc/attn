package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) resolveSpawnParent(spawnedFrom string, profile profiles.Profile, placement *launchPlacement, isShell bool) string {
	if !isShell || placement == nil {
		return ""
	}
	parentID := protocol.SessionID(d.satelliteParentOf(protocol.SessionID(strings.TrimSpace(spawnedFrom))))
	if parentID == "" {
		return ""
	}
	parentPlacement, placed, err := d.store.SessionPlacement(parentID)
	if err != nil || !placed || parentPlacement.DesktopID != placement.targetDesktop(profile) {
		return ""
	}
	return string(parentID)
}

func (d *Daemon) satelliteParentOf(baseID protocol.SessionID) string {
	if baseID == "" {
		return ""
	}
	base := d.store.Get(baseID)
	if base == nil {
		return ""
	}
	parentID := base.ID
	if base.Agent == protocol.AgentShellValue {
		parentID = protocol.TrimID(protocol.Deref(base.ParentSessionID))
	}
	if parentID == "" || d.store.Get(parentID) == nil {
		return ""
	}
	return string(parentID)
}
