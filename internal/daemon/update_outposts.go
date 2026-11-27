package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const homeUpdateNoticeTimeout = 5 * time.Second

type homeUpdateNotice struct {
	mu     sync.Mutex
	run    string
	target string
}

func (d *Daemon) setUpdateOutpost(run store.UpdateRun, row store.Machine, state, detail string) {
	d.setUpdateRow(run, store.UpdateRunOutpost{ID: row.ID, Name: row.Name, Role: store.UpdateRoleOutpost, State: state, Detail: detail})
}

func (d *Daemon) failUpdateOutpost(run store.UpdateRun, row store.Machine, err error) {
	d.setUpdateRow(run, failedRow(store.UpdateRunOutpost{ID: row.ID, Name: row.Name, Role: store.UpdateRoleOutpost}, err))
}

// stageOutposts readies every outpost before the home switches and returns the
// failures that must stop a run in which the home has not restarted yet. The
// app machine that started the run has a row of its own.
func (d *Daemon) stageOutposts(ctx context.Context, run store.UpdateRun, target bridge.Build, without []string, appMachine string) []string {
	if !d.outpostsEnabled() {
		return nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return []string{fmt.Sprintf("the outposts: %v", err)}
	}
	var failed []string
	for _, row := range rows {
		if row.ID == appMachine {
			continue
		}
		if slices.Contains(without, row.ID) {
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, "Left out of this update.")
			continue
		}
		if row.HostKey == "" {
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, "Not paired yet.")
			continue
		}
		if row.Reverse() {
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, "It dials this home; update attn on that machine.")
			continue
		}
		d.setUpdateOutpost(run, row, store.UpdateOutpostStaging, "connecting")
		skip, err := d.stageOutpost(ctx, run, row, target)
		switch {
		case skip != nil:
			d.setUpdateRow(run, store.UpdateRunOutpost{ID: row.ID, Name: row.Name, Role: store.UpdateRoleOutpost, State: store.UpdateOutpostSkipped, Detail: "Unreachable: " + skip.Error(), Reason: string(protocol.UpdateFailureReasonUnreachable), Action: actionUpdate})
		case err != nil:
			d.failUpdateOutpost(run, row, err)
			failed = append(failed, row.Name+": "+err.Error())
		default:
			d.setUpdateOutpost(run, row, store.UpdateOutpostStaged, "")
		}
	}
	return failed
}

// stageOutpost returns skip when the outpost cannot be reached, and err, with its reason, when it cannot be staged.
func (d *Daemon) stageOutpost(ctx context.Context, run store.UpdateRun, row store.Machine, target bridge.Build) (skip, err error) {
	remote := d.outpostSSH(row)
	if err := remote.EnsureMaster(ctx); err != nil {
		return err, nil
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		var sshError *outpostssh.Error
		if errors.As(err, &sshError) {
			return err, nil
		}
		return nil, fault(string(protocol.UpdateFailureReasonRefused), err)
	}
	if err := probe.Enrollment.CheckHome(d.enrollmentHome()); err != nil {
		return nil, fault(string(protocol.UpdateFailureReasonRefused), err)
	}
	if probe.Build != nil && *probe.Build == target && !probe.HostMissing {
		return nil, nil
	}
	phase := func(step string) { d.setUpdateOutpost(run, row, store.UpdateOutpostStaging, step) }
	binaries, err := d.outpostBinaries(ctx, target, probe.Platform, phase)
	if err != nil {
		return nil, fault(string(protocol.UpdateFailureReasonNoBuild), err)
	}
	if err := remote.Stage(ctx, binaries, target, phase); err != nil {
		return nil, sshFault(err)
	}
	return nil, nil
}

func (d *Daemon) noticeOutposts(ctx context.Context, run store.UpdateRun, target bridge.Build) {
	d.sendOutpostNotices(ctx, run, target.String())
}

// withdrawOutpostNotices tells staged outposts the home is staying up. The
// bridge never dropped, so no new hello would clear their notice.
func (d *Daemon) withdrawOutpostNotices(run store.UpdateRun) {
	d.sendOutpostNotices(d.life.Context(), run, "")
}

func (d *Daemon) sendOutpostNotices(ctx context.Context, run store.UpdateRun, target string) {
	latest := d.latestUpdateRun()
	if latest == nil {
		return
	}
	for _, outpost := range latest.Outposts {
		if outpost.State != store.UpdateOutpostStaged {
			continue
		}
		notice, cancel := context.WithTimeout(ctx, homeUpdateNoticeTimeout)
		_, err := Call[struct{}](notice, d.router(), protocol.HomeUpdateNoticeMessage{Cmd: protocol.CmdHomeUpdateNotice, Host: outpost.ID, Run: run.ID, TargetBuild: target})
		cancel()
		if err != nil {
			d.logf("update run %s: notice to %s: %v", run.ID, outpost.Name, err)
		}
	}
}

func (d *Daemon) receiveHomeUpdateNotice(_ context.Context, msg protocol.HomeUpdateNoticeMessage) (struct{}, error) {
	n := &d.homeUpdate
	n.mu.Lock()
	n.run, n.target = msg.Run, msg.TargetBuild
	n.mu.Unlock()
	return struct{}{}, nil
}

func (d *Daemon) clearHomeUpdateNotice() {
	n := &d.homeUpdate
	n.mu.Lock()
	n.run, n.target = "", ""
	n.mu.Unlock()
}

func (d *Daemon) currentHomeUpdateNotice() (run, target string) {
	n := &d.homeUpdate
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.run, n.target
}

func (d *Daemon) homeUpdateNoticeText(home string) string {
	_, target := d.currentHomeUpdateNotice()
	if target == "" {
		return ""
	}
	return fmt.Sprintf("%s is restarting to update attn to %s; try again in a moment", home, target)
}

func (d *Daemon) answerUpdateStatusFromNotice(conn net.Conn) bool {
	run, target := d.currentHomeUpdateNotice()
	if target == "" {
		return false
	}
	status := d.updateStatus()
	status.Run = &protocol.UpdateRun{ID: run, TargetBuild: target, Phase: protocol.UpdatePhaseApplying, Requesters: []string{}, Outposts: []protocol.UpdateOutpostProgress{}}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, UpdateStatus: &status})
	return true
}

// switchOutposts moves the staged outposts onto this daemon's build. It runs
// after the home has restarted, so a failure here leaves the home updated. It
// returns the text to add to the run's outcome.
func (d *Daemon) switchOutposts(run store.UpdateRun) string {
	latest := d.latestUpdateRun()
	if latest == nil {
		return ""
	}
	var wait sync.WaitGroup
	for _, outpost := range latest.Outposts {
		if outpost.State != store.UpdateOutpostStaged && outpost.State != store.UpdateOutpostSwitching {
			continue
		}
		row, err := d.resolveOutpost(outpost.ID)
		if err != nil {
			d.logf("update run %s: %v", run.ID, err)
			continue
		}
		wait.Add(1)
		if !d.life.Go("switchOutpost:"+row.ID, func() {
			defer wait.Done()
			d.setUpdateOutpost(run, row, store.UpdateOutpostSwitching, "")
			if err := d.switchOutpost(d.life.Context(), row); err != nil {
				d.failUpdateOutpost(run, row, err)
				return
			}
			d.setUpdateRow(run, store.UpdateRunOutpost{ID: row.ID, Name: row.Name, Role: store.UpdateRoleOutpost, State: store.UpdateOutpostDone, Build: d.bridgeIdentity().Build.String()})
		}) {
			wait.Done()
		}
	}
	wait.Wait()
	final := d.latestUpdateRun()
	if final == nil {
		return ""
	}
	var skipped, failed []string
	for _, outpost := range final.Outposts {
		switch outpost.State {
		case store.UpdateOutpostSkipped:
			skipped = append(skipped, outpost.Name)
		case store.UpdateOutpostFailed:
			failed = append(failed, outpost.Name+": "+outpost.Detail)
		}
	}
	var summary strings.Builder
	if len(skipped) > 0 {
		summary.WriteString(" Skipped " + strings.Join(skipped, ", ") + "; use Update outposts when they are back.")
	}
	if len(failed) > 0 {
		summary.WriteString(" Could not switch " + strings.Join(failed, "; ") + ".")
	}
	return summary.String()
}

func (d *Daemon) switchOutpost(ctx context.Context, row store.Machine) error {
	remote := d.outpostSSH(row)
	if err := remote.EnsureMaster(ctx); err != nil {
		return fault(string(protocol.UpdateFailureReasonUnreachable), err)
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		return sshFault(err)
	}
	self := d.bridgeIdentity().Build
	if probe.Build == nil || *probe.Build != self || probe.HostMissing {
		binaries, err := d.outpostBinaries(ctx, self, probe.Platform, func(string) {})
		if err != nil {
			return fault(string(protocol.UpdateFailureReasonNoBuild), err)
		}
		if err := remote.Switch(ctx, binaries, self, true, nil); err != nil {
			return sshFault(err)
		}
	}
	return fault(string(protocol.UpdateFailureReasonNotBack), d.reconnectOutpostOnHomeBuild(ctx, row))
}
