package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const bridgeCallRecovery = "recover"

type outpostLiveTerminal struct {
	Info     ptybackend.SessionInfo
	HaveInfo bool
}
type outpostProbe struct {
	Alive bool
	Error string
}
type outpostMachine struct {
	NotebookRoot string
	Live         map[harness.TerminalID]outpostLiveTerminal
	Probes       map[harness.TerminalID]outpostProbe
}

func (d *Daemon) outpostMachineReport() outpostMachine {
	machine := d.recoverMachine(d.life.Context())
	report := outpostMachine{Live: make(map[harness.TerminalID]outpostLiveTerminal), Probes: make(map[harness.TerminalID]outpostProbe)}
	for _, session := range d.store.List("") {
		if d.copiedSession(session.ID) {
			for _, terminal := range d.terminalsOf(session.ID) {
				report.Probes[terminal] = d.recoveredTerminal(terminal)
			}
		}
	}
	for id, live := range machine.live {
		if d.copiedSession(id) {
			report.Live[live.terminal] = outpostLiveTerminal{Info: live.info, HaveInfo: live.haveInfo}
		}
	}
	report.NotebookRoot, _ = d.notebookRoot()
	d.monitors().notifyViewsChanged()
	return report
}

func (d *Daemon) runningSessionTerminals(session protocol.SessionID) []harness.TerminalID {
	running := []harness.TerminalID{}
	for _, terminal := range d.terminalsOf(session) {
		probe := d.recoveredTerminal(terminal)
		if probe.Alive || probe.Error != "" {
			running = append(running, terminal)
		}
	}
	return running
}

func (d *Daemon) recoveredTerminal(terminal harness.TerminalID) outpostProbe {
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return outpostProbe{Error: "terminal state is unavailable"}
	}
	info, err := provider.SessionInfo(d.life.Context(), terminal)
	if err == nil {
		return outpostProbe{Alive: info.Running}
	}
	if prober, ok := d.ptyBackend.(ptybackend.SessionLivenessProber); ok {
		alive, probeErr := prober.SessionLikelyAlive(d.life.Context(), terminal)
		if probeErr == nil {
			return outpostProbe{Alive: alive}
		}
		return outpostProbe{Error: probeErr.Error()}
	}
	if errors.Is(err, pty.ErrSessionNotFound) {
		return outpostProbe{}
	}
	return outpostProbe{Error: err.Error()}
}

func (d *Daemon) recoverOutpost(ctx context.Context, host string, conn *bridge.Conn) error {
	pausepoint.At(pausepoint.OutpostRecoveryStarted)
	started := time.Now()
	var sessions []protocol.SessionID
	previous := map[protocol.SessionID]struct{}{}
	placements := map[protocol.SessionID][]harness.TerminalID{}
	for _, session := range d.store.List("") {
		if protocol.Deref(session.Host) != host {
			continue
		}
		sessions = append(sessions, session.ID)
		placements[session.ID] = d.terminalsOf(session.ID)
		if !d.hidden(session.ID) {
			previous[session.ID] = struct{}{}
		}
	}
	var report outpostMachine
	if err := conn.Call(ctx, bridgeCallRecovery, struct{}{}, &report); err != nil {
		return err
	}
	pausepoint.At(pausepoint.OutpostRecoveryReported)
	d.outposts.mu.Lock()
	if run := d.outposts.dialers[host]; run != nil && run.conn == conn {
		run.notebookRoot = report.NotebookRoot
	}
	d.outposts.mu.Unlock()
	machine := machineReport{live: make(map[protocol.SessionID]liveTerminal), probes: make(map[protocol.SessionID]livenessProbe)}
	for _, id := range sessions {
		for _, terminal := range placements[id] {
			if live, ok := report.Live[terminal]; ok {
				machine.live[id] = liveTerminal{terminal: terminal, info: live.Info, haveInfo: live.HaveInfo}
			}
			if probe, ok := report.Probes[terminal]; ok {
				result := machine.probes[id]
				result.likelyAlive = result.likelyAlive || probe.Alive
				if probe.Error != "" {
					result.err = fmt.Errorf("%s", probe.Error)
				}
				machine.probes[id] = result
			}
		}
	}
	d.recoverRecords(machine, sessions, recoveryRules{host: host, allowIdleDemotion: true, allowTombstoneCleanup: true, previousRunSessions: previous, placements: placements, startedAt: started})
	running := make([]harness.TerminalID, 0, len(report.Live)+len(report.Probes))
	for terminal := range report.Live {
		running = append(running, terminal)
	}
	for terminal, probe := range report.Probes {
		if _, live := report.Live[terminal]; !live && (probe.Alive || probe.Error != "") {
			running = append(running, terminal)
		}
	}
	for _, session := range sessions {
		if err := d.reconcileRemoteTerminals(ctx, session, running); err != nil {
			return err
		}
	}
	pausepoint.At(pausepoint.OutpostRecoveryApplied)
	return nil
}

func (d *Daemon) kickOutpostInboxes(host string) {
	for _, session := range d.store.List("") {
		if protocol.Deref(session.Host) == host {
			d.kickSessionInboxAddresses(session.ID)
		}
	}
}
