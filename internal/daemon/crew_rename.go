package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleCrewRename(conn net.Conn, msg *protocol.CrewRenameMessage) {
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		d.sendError(conn, bindingsErr.Error())
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
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
	if err := crew.ValidateName(name); err != nil && !(errors.Is(err, crew.ErrNameReservedForChief) && d.isChief(identity.Key)) {
		d.sendCrewError(conn, "rename", err)
		return
	}
	member, doc, err := d.crewMember(identity.Key)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	previous := identity.Name
	legacy, err := d.legacyCrewHome(member.HomeDir)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	if member.HomeDir != "" && !legacy && !strings.EqualFold(previous, name) {
		identity.Name = name
		_, _, err = d.relocateCrewHome(identity, member, doc.Rev, filepath.Dir(member.HomeDir))
	} else {
		previous, err = d.store.RenameCrewMember(identity.Key, name)
	}
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	d.publishFact(FactCrewUpdated, identity.Key.String(), nil)
	result := &protocol.CrewRenameResult{Member: identity.Key.String(), Name: name, PreviousName: previous}
	member, _, err = d.crewMember(identity.Key)
	if err == nil && d.crewBindingLive(member) {
		d.store.UpdateSessionLabel(member.BindingSession, name)
		d.publishFact(FactSessionRenamed, string(member.BindingSession), nil)
		result.SessionID = protocol.Ptr(member.BindingSession)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRenameResult: result})
}
