package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHandleUnregisterWS_RefusesChiefOfStaff(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	if err := setTestChief(d, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "chief"})

	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session was unregistered despite the close guard")
	}
	if got := d.chiefForCaller(""); got != "chief" {
		t.Fatalf("chief role after refused close = %q, want chief", got)
	}
	expectCommandError(t, client, protocol.CmdUnregister, errChiefOfStaffProtected.Error())
}

func TestHandleUnregisterWS_AllowsNonChiefWhileChiefExists(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	if err := setTestChief(d, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "worker"})

	if d.store.Get("worker") != nil {
		t.Fatal("ordinary session was not unregistered while a chief existed")
	}
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session must survive a sibling's close")
	}
}

func TestHandleUnregisterWS_RefusesCrewMember(t *testing.T) {
	d := newCrewDaemon(t)
	client := newRenameTestClient()
	addSession(t, d, "trellis-day")
	if _, err := d.claimCrewBinding("trellis", "trellis-day"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "trellis-day"})

	if d.store.Get("trellis-day") == nil {
		t.Fatal("crew member's session was unregistered despite the close guard")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "trellis-day" {
		t.Fatalf("crew binding after refused close = %q, want trellis-day", got)
	}
	expectCommandError(t, client, protocol.CmdUnregister, "Trellis is protected from closing; put Trellis to sleep first")
}
