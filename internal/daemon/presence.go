package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func isUserPresenceCommand(cmd string) bool {
	switch cmd {
	case protocol.CmdSessionSelected,
		protocol.CmdWorkspaceSelected,
		protocol.CmdPRVisited,
		protocol.CmdPtyInput,
		protocol.CmdPtyResize:
		return true
	default:
		return false
	}
}

func (d *Daemon) recordUserActivity(now time.Time) {
	d.lastUserActivityAtNano.Store(now.UnixNano())
}

func (d *Daemon) lastUserActivityAt() time.Time {
	nano := d.lastUserActivityAtNano.Load()
	if nano == 0 {
		return time.Time{}
	}
	return time.Unix(0, nano).UTC()
}
