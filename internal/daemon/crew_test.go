package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
)

func writeCrewHomes(t *testing.T, dataRoot string) {
	t.Helper()
	root := filepath.Join(dataRoot, crew.HomesDirName)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(root, "CREW.md"), "# The crew\n")
	for _, member := range []struct{ id, handoff string }{
		{"alder", "2026-08-10T19-20Z-alder.md"},
		{"keel", "2026-08-13T22-10Z-keel.md"},
		{"trellis", "2026-08-13T22-20Z-trellis.md"},
	} {
		home := filepath.Join(root, member.id)
		write(filepath.Join(home, crew.CharterFileName), "# "+member.id+"\n\nWhat I care about.\n")
		write(filepath.Join(home, "handoffs", member.handoff), "Where I left off.\n")
	}
}

func newCrewDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	return d
}

func crewList(t *testing.T, d *Daemon) []protocol.CrewMember {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleCrewList(c, &protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	})
	if !resp.Ok {
		t.Fatalf("crew list: %v", protocol.Deref(resp.Error))
	}
	return resp.CrewListResult.Members
}

func addSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func memberByID(t *testing.T, members []protocol.CrewMember, id string) protocol.CrewMember {
	t.Helper()
	for _, m := range members {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no member %q in the roster", id)
	return protocol.CrewMember{}
}

func TestCrew_AnOutpostIsFenced(t *testing.T) {
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()

	resp := gardenCall(t, func(c net.Conn) {
		d.handleCrewList(c, &protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	})
	if resp.Ok {
		t.Fatal("an outpost served the crew roster")
	}
	message := protocol.Deref(resp.Error)
	if !strings.Contains(message, home) {
		t.Errorf("refusal %q does not name the home", message)
	}
	if !strings.Contains(message, enrollment.PlanPath) {
		t.Errorf("refusal %q does not name the plan tracking the gap", message)
	}

	addSession(t, d, "sess-a")
	if _, err := d.claimCrewBinding("keel", "sess-a"); err == nil {
		t.Fatal("an outpost bound a crew member")
	}
}
