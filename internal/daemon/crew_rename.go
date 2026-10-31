package daemon

import (
	"errors"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

var errCrewRenameNeedsV2 = errors.New("renaming crew members is part of crew v2, which is off; `attn settings set crew_v2_enabled true` turns it on")

func (d *Daemon) crewRename(msg *protocol.CrewRenameMessage) (*protocol.CrewRenameResult, error) {
	if !d.crewV2Enabled() {
		return nil, errCrewRenameNeedsV2
	}
	member, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		return nil, err
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	name := strings.TrimSpace(msg.Name)
	previous, err := d.store.RenameCrewMember(member.ID, name)
	if err != nil {
		return nil, err
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.publishArrangementChanged(member.ProfileID)
	d.logf("crew: member %s renamed from %s to %s", member.ID, previous, name)
	result := &protocol.CrewRenameResult{Member: member.ID, Name: name, PreviousName: previous}
	if d.crewBindingLive(member) {
		if err := d.renameSession(&protocol.RenameSessionMessage{SessionID: member.BindingSession, Label: name}); err != nil {
			d.logf("crew: %s's session %s keeps its label: %v", name, member.BindingSession, err)
		} else {
			result.SessionID = protocol.Ptr(member.BindingSession)
		}
	}
	return result, nil
}

func (d *Daemon) handleCrewRename(conn net.Conn, msg *protocol.CrewRenameMessage) {
	result, err := d.crewRename(msg)
	if err != nil {
		d.sendCrewError(conn, "rename", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRenameResult: result})
}

func (d *Daemon) handleCrewRenameWS(client *wsClient, msg *protocol.CrewRenameMessage) {
	result, err := d.crewRename(msg)
	response := protocol.CrewRenameResultMessage{Event: protocol.EventCrewRenameResult, RequestID: protocol.Deref(msg.RequestID), Success: err == nil, Result: result}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
