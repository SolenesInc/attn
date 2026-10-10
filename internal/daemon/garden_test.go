package daemon

import (
	"encoding/json"
	"github.com/victorarias/attn/internal/protocol"
	"net"
	"testing"
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
func plant(t *testing.T, d *Daemon, msg protocol.SeedPlantMessage) protocol.Seed {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlant
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", msg.Title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}
