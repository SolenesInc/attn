package testworld

import (
	"github.com/victorarias/attn/internal/protocol"
	"testing"
)

type OutpostPair struct {
	Home, Outpost     *Stack
	SSH               *SSH
	App               *Peer
	Host, Destination string
}

func (p *OutpostPair) Disconnect() {
	p.SSH.Fail(p.Destination, "ssh: connect to host machine port 22: No route to host")
	p.SSH.ExitMaster(p.Home, p.Host)
}

func (p *OutpostPair) Reconnect() {
	p.SSH.Restore(p.Destination)
}

func PrepareOutpostPair(t *testing.T, home, outpost *Stack) *OutpostPair {
	t.Helper()
	ssh := NewSSH(t)
	ssh.Use(home.World)
	return &OutpostPair{Home: home, Outpost: outpost, SSH: ssh, Destination: "outpost"}
}
func (p *OutpostPair) Start() {
	p.Home.T.Helper()
	p.Outpost.Start()
	p.Home.Start()
	p.Connect()
}
func (p *OutpostPair) Connect() {
	p.Home.T.Helper()
	p.SSH.Route(p.Destination, p.Outpost)
	if result := p.Home.Attn("settings", "set", "outposts_enabled", "true"); result.Code != 0 {
		p.Home.T.Fatalf("enable outposts: %+v", result)
	}
	p.App = p.Home.App()
	p.App.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: p.Destination})
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
}
