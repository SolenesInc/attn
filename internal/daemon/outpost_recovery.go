package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const bridgeCallRecovery = "recover"

type outpostLiveTerminal struct {
	Terminal harness.TerminalID
	Info     ptybackend.SessionInfo
	HaveInfo bool
}
type outpostProbe struct {
	Alive bool
	Error string
}
type outpostMachine struct {
	Live   map[protocol.SessionID]outpostLiveTerminal
	Probes map[protocol.SessionID]outpostProbe
}

func (d *Daemon) outpostMachineReport() outpostMachine {
	machine := d.recoverMachine(d.life.Context())
	report := outpostMachine{Live: make(map[protocol.SessionID]outpostLiveTerminal), Probes: make(map[protocol.SessionID]outpostProbe)}
	for id, live := range machine.live {
		if d.copiedSession(id) {
			report.Live[id] = outpostLiveTerminal{Terminal: live.terminal, Info: live.info, HaveInfo: live.haveInfo}
		}
	}
	for id, probe := range machine.probes {
		if !d.copiedSession(id) {
			continue
		}
		result := outpostProbe{Alive: probe.likelyAlive}
		if probe.err != nil {
			result.Error = probe.err.Error()
		}
		report.Probes[id] = result
	}
	d.monitors().notifyViewsChanged()
	return report
}

func (d *Daemon) recoverOutpost(ctx context.Context, host string, conn *bridge.Conn) error {
	started := time.Now()
	var report outpostMachine
	if err := conn.Call(ctx, bridgeCallRecovery, struct{}{}, &report); err != nil {
		return err
	}
	machine := machineReport{live: make(map[protocol.SessionID]liveTerminal), probes: make(map[protocol.SessionID]livenessProbe)}
	var sessions []protocol.SessionID
	previous := map[protocol.SessionID]struct{}{}
	for _, session := range d.store.List("") {
		if protocol.Deref(session.Host) != host {
			continue
		}
		sessions = append(sessions, session.ID)
		previous[session.ID] = struct{}{}
		if live, ok := report.Live[session.ID]; ok {
			machine.live[session.ID] = liveTerminal{terminal: live.Terminal, info: live.Info, haveInfo: live.HaveInfo}
		}
		if probe, ok := report.Probes[session.ID]; ok {
			result := livenessProbe{likelyAlive: probe.Alive}
			if probe.Error != "" {
				result.err = fmt.Errorf("%s", probe.Error)
			}
			machine.probes[session.ID] = result
		}
	}
	d.recoverRecords(machine, sessions, recoveryRules{host: host, allowIdleDemotion: true, allowTombstoneCleanup: true, previousRunSessions: previous, startedAt: started})
	return nil
}
