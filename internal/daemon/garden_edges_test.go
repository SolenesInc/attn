package daemon

import (
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func link(t *testing.T, d *Daemon, from, kind, to string, unlink bool) protocol.Response {
	t.Helper()
	msg := protocol.SeedLinkMessage{Cmd: protocol.CmdSeedLink, SeedID: from, Kind: kind, ToSeedID: to}
	if unlink {
		msg.Unlink = protocol.Ptr(true)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedLink(c, &msg) })
}

func mustLink(t *testing.T, d *Daemon, from, kind, to string) protocol.SeedLinkResult {
	t.Helper()
	resp := link(t, d, from, kind, to, false)
	if !resp.Ok {
		t.Fatalf("link %s %s %s: %v", from, kind, to, protocol.Deref(resp.Error))
	}
	return *resp.SeedLinkResult
}

func ready(t *testing.T, d *Daemon, msg protocol.SeedReadyMessage) protocol.SeedReadyResult {
	t.Helper()
	msg.Cmd = protocol.CmdSeedReady
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedReady(c, &msg) })
	if !resp.Ok {
		t.Fatalf("ready: %v", protocol.Deref(resp.Error))
	}
	return *resp.SeedReadyResult
}

func readyIDs(result protocol.SeedReadyResult) []string {
	out := make([]string, 0, len(result.Seeds))
	for _, seed := range result.Seeds {
		out = append(out, seed.ID)
	}
	return out
}

func TestGardenEdges_ReadyFallsBackWhenTheCrownIsGone(t *testing.T) {
	d := newGardenDaemon(t)
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "still here"})
	if err := d.recordGardenDispatch("sess-a", "s-zzzzzz", "", "", "", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	result := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if result.Scope != "garden" || len(result.Seeds) != 1 {
		t.Fatalf("ready with a dangling dispatch = %+v, want the whole garden", result)
	}
}
