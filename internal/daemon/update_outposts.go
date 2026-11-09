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

func (d *Daemon) setUpdateOutpost(run store.UpdateRun, row store.Outpost, state, detail string) {
	if err := d.store.SetUpdateRunOutpost(run.ID, store.UpdateRunOutpost{ID: row.ID, Name: row.Name, State: state, Detail: detail}); err != nil {
		d.logf("update run %s: record outpost %s: %v", run.ID, row.Name, err)
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
}

// stageOutposts readies every outpost before the home restarts and returns the
// failures that must stop the run.
func (d *Daemon) stageOutposts(ctx context.Context, run store.UpdateRun, target bridge.Build, without []string) []string {
	if !d.outpostsEnabled() {
		return nil
	}
	rows, err := d.store.ListOutposts()
	if err != nil {
		return []string{fmt.Sprintf("the outposts: %v", err)}
	}
	var failed []string
	for _, row := range rows {
		if slices.Contains(without, row.ID) {
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, "Left out of this update.")
			continue
		}
		if row.HostKey == "" {
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, "Not paired yet.")
			continue
		}
		d.setUpdateOutpost(run, row, store.UpdateOutpostStaging, "connecting")
		skip, err := d.stageOutpost(ctx, run, row, target)
		switch {
		case skip != "":
			d.setUpdateOutpost(run, row, store.UpdateOutpostSkipped, skip)
		case err != nil:
			d.setUpdateOutpost(run, row, store.UpdateOutpostFailed, err.Error())
			failed = append(failed, row.Name+": "+err.Error())
		default:
			d.setUpdateOutpost(run, row, store.UpdateOutpostStaged, "")
		}
	}
	return failed
}

func (d *Daemon) stageOutpost(ctx context.Context, run store.UpdateRun, row store.Outpost, target bridge.Build) (skip string, err error) {
	remote := d.outpostSSH(row)
	if err := remote.EnsureMaster(ctx); err != nil {
		return "Unreachable: " + err.Error(), nil
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		var sshError *outpostssh.Error
		if errors.As(err, &sshError) {
			return "Unreachable: " + err.Error(), nil
		}
		return "", err
	}
	if err := probe.Enrollment.CheckHome(d.daemonInstanceID); err != nil {
		return "", err
	}
	if probe.Build != nil && *probe.Build == target && !probe.HostMissing {
		return "", nil
	}
	phase := func(step string) { d.setUpdateOutpost(run, row, store.UpdateOutpostStaging, step) }
	binaries, err := d.outpostBinaries(ctx, target, probe.Platform, phase)
	if err != nil {
		return "", err
	}
	return "", remote.Stage(ctx, binaries, target, phase)
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
				d.setUpdateOutpost(run, row, store.UpdateOutpostFailed, err.Error())
				return
			}
			d.setUpdateOutpost(run, row, store.UpdateOutpostDone, "")
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

func (d *Daemon) switchOutpost(ctx context.Context, row store.Outpost) error {
	remote := d.outpostSSH(row)
	if err := remote.EnsureMaster(ctx); err != nil {
		return err
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		return err
	}
	self := d.bridgeIdentity().Build
	if probe.Build == nil || *probe.Build != self || probe.HostMissing {
		binaries, err := d.outpostBinaries(ctx, self, probe.Platform, func(string) {})
		if err != nil {
			return err
		}
		if err := remote.Switch(ctx, binaries, self, true); err != nil {
			return err
		}
	}
	return d.reconnectOutpostOnHomeBuild(ctx, row)
}
