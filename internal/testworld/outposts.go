package testworld

import (
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

type OutpostPair struct {
	Home, Outpost *Stack
	Agent         *SSHAgent
	App           *Peer
	Host, Address string
}

func PrepareOutpostPair(t *testing.T, home, outpost *Stack) *OutpostPair {
	t.Helper()
	a := NewSSHAgent(t)
	a.Authorize(home.World)
	a.Authorize(outpost.World)
	return &OutpostPair{Home: home, Outpost: outpost, Agent: a}
}

func (p *OutpostPair) Start() {
	p.Home.T.Helper()
	p.Outpost.Start()
	p.Home.Start()
	p.Connect()
}

func (p *OutpostPair) Connect() {
	p.Home.T.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		p.Home.T.Fatal(err)
	}
	p.Address = listener.Addr().String()
	_ = listener.Close()
	for _, step := range []struct {
		stack      *Stack
		key, value string
	}{{p.Outpost, "outpost_listen", p.Address}, {p.Home, "outposts_enabled", "true"}} {
		if result := step.stack.Attn("settings", "set", step.key, step.value); result.Code != 0 {
			p.Home.T.Fatalf("set %s: %+v", step.key, result)
		}
	}
	p.App = p.Home.App()
	outpostApp := p.Outpost.App()
	p.App.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: p.Address})
	update := Await(p.App, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		for _, row := range m.Outposts {
			if row.Name == "outpost" && row.State == protocol.OutpostStateConnected {
				return true
			}
		}
		return false
	})
	for _, row := range update.Outposts {
		if row.Name == "outpost" {
			p.Host = row.ID
		}
	}
	Await(outpostApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == "bridged"
	})
}
