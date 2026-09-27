package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func gardenCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func newGardenDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-a", Label: "a",
		State: "idle", StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	return d
}

func plant(t *testing.T, d *Daemon, msg protocol.SeedPlantMessage) protocol.Seed {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlant
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", msg.Title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

func addGardenSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func move(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Seed {
	t.Helper()
	resp := transition(t, d, session, seedID, verb, reason, member)
	if !resp.Ok {
		t.Fatalf("%s %s: %v", verb, seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedTransitionResult.Seed
}

func transition(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(verb),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}
