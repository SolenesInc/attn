package daemon

import (
	"net"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleCrewRename(conn net.Conn, msg *protocol.CrewRenameMessage) {
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	identity, err := d.resolveMember(r, msg.Member)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	name := strings.TrimSpace(msg.Name)
	previous, err := d.store.RenameCrewMember(identity.Key, name)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	d.publishFact(FactCrewUpdated, identity.Key.String(), nil)
	result := &protocol.CrewRenameResult{Member: identity.Key.String(), Name: name, PreviousName: previous}
	member, _, err := d.crewMember(identity.Key)
	if err == nil && d.crewBindingLive(member) {
		d.store.UpdateSessionLabel(member.BindingSession, name)
		d.publishFact(FactSessionRenamed, string(member.BindingSession), nil)
		result.SessionID = protocol.Ptr(member.BindingSession)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRenameResult: result})
}
