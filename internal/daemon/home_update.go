package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/outpostbuild"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/selfupdate"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

// installKindHome marks a run in which this app machine pushes its build to its home.
const installKindHome = "home"

// installKindFleet marks a run at the home that brings its outposts to the build an app machine pushed.
const installKindFleet = "fleet"

// rejoinOnNewBuild bounds the wait for a machine to come back on the build it switched to. Measured on an outpost
// (s-wcd4g4 C65): 2.41 s from restart to connected; a home restarts the same way.
const rejoinOnNewBuild = time.Minute

const HomeRejoinTimer stepclock.Name = "home-update-rejoin"

const actionUpdate = "Update"

// sharedBuild is the app machine's one record (plot D5): the build it ran when its bridge to its home last connected.
type sharedBuild struct {
	HomeID string       `json:"home_id"`
	Build  bridge.Build `json:"build"`
}

func (d *Daemon) sharedBuildPath() string { return filepath.Join(d.dataRoot, "home-shared-build.json") }

func (d *Daemon) readSharedBuild() (sharedBuild, bool) {
	data, err := os.ReadFile(d.sharedBuildPath())
	if err != nil {
		return sharedBuild{}, false
	}
	var record sharedBuild
	if json.Unmarshal(data, &record) != nil {
		return sharedBuild{}, false
	}
	return record, true
}

func (d *Daemon) recordSharedBuild(record sharedBuild) {
	if current, ok := d.readSharedBuild(); ok && current == record {
		return
	}
	data, err := json.Marshal(record)
	if err == nil {
		err = os.WriteFile(d.sharedBuildPath()+".tmp", data, 0o600)
	}
	if err == nil {
		err = os.Rename(d.sharedBuildPath()+".tmp", d.sharedBuildPath())
	}
	if err != nil {
		d.logf("record the build shared with home %s: %v", record.HomeID, err)
	}
}

func (d *Daemon) forgetSharedBuild() {
	if err := os.Remove(d.sharedBuildPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.logf("forget the build shared with the home: %v", err)
	}
}

// updateFault is a failure of one machine in an update run, with the reason the run shows for it (plot D12).
type updateFault struct {
	reason string
	err    error
}

func (f *updateFault) Error() string { return f.err.Error() }
func (f *updateFault) Unwrap() error { return f.err }

func fault(reason string, err error) error {
	if err == nil {
		return nil
	}
	var known *updateFault
	if errors.As(err, &known) {
		return err
	}
	return &updateFault{reason: reason, err: err}
}

// sshFault reads a failed ssh step: ssh itself exits 255 when it cannot reach the machine; any other exit is the
// remote command refusing.
func sshFault(err error) error {
	var failed *outpostssh.Error
	if errors.As(err, &failed) && failed.ExitCode != 255 {
		return fault(string(protocol.UpdateFailureReasonRefused), err)
	}
	if errors.As(err, &failed) || errors.Is(err, context.DeadlineExceeded) {
		return fault(string(protocol.UpdateFailureReasonUnreachable), err)
	}
	return fault(string(protocol.UpdateFailureReasonRefused), err)
}

func reasonOf(err error) string {
	var known *updateFault
	if errors.As(err, &known) {
		return known.reason
	}
	return string(protocol.UpdateFailureReasonRefused)
}

func (d *Daemon) setUpdateRow(run store.UpdateRun, row store.UpdateRunOutpost) {
	if err := d.store.SetUpdateRunOutpost(run.ID, row); err != nil {
		d.logf("update run %s: record %s: %v", run.ID, row.Name, err)
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
}

func failedRow(row store.UpdateRunOutpost, err error) store.UpdateRunOutpost {
	row.State, row.Detail, row.Reason, row.Action = store.UpdateOutpostFailed, err.Error(), reasonOf(err), actionUpdate
	return row
}

// homeBridged runs when this machine's dial reaches its home: the builds are equal, so this is the build they share.
func (d *Daemon) homeBridged() {
	status := d.enrollmentStatus()
	d.recordSharedBuild(sharedBuild{HomeID: status.HomeID, Build: d.bridgeIdentity().Build})
	r := &d.outpost
	r.mu.Lock()
	r.refusedBy = nil
	if r.homeBridged != nil {
		close(r.homeBridged)
		r.homeBridged = nil
	}
	r.mu.Unlock()
}

func (d *Daemon) homeBridgedSignal() <-chan struct{} {
	r := &d.outpost
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.homeBridged == nil {
		r.homeBridged = make(chan struct{})
	}
	return r.homeBridged
}

// homeRefusedByBuild decides what a build refusal from the home starts. Only an app machine whose build changed
// while its home stayed on the build they shared updates the home by itself; the record goes as the update starts,
// so a failed update cannot start another. It returns the refusal as the dial shows it.
func (d *Daemon) homeRefusedByBuild(destination string, home bridge.Identity, refusal error) error {
	r := &d.outpost
	r.mu.Lock()
	r.refusedBy = &home
	r.mu.Unlock()
	self := d.bridgeIdentity().Build
	status := d.enrollmentStatus()
	record, recorded := d.readSharedBuild()
	shared := recorded && record.HomeID == status.HomeID
	d.logf("home %s refused build %s with its build %s; the build they last shared: %s", home.Name, self, home.Build, record.Build)
	updates := d.on(setting.SelfUpdateEnabled)
	switch {
	case shared && record.Build == home.Build && record.Build != self && updates:
		d.forgetSharedBuild()
		if _, err := d.startHomeUpdate(destination, home); err != nil {
			d.logf("update home %s: %v", home.Name, err)
		}
		return refusal
	case shared && record.Build == self:
		command := updateThisMachineCommand(home.Build)
		if updates {
			d.recordHomeDifference(home, true, command)
		}
		return fmt.Errorf("%s runs build %s, newer than this machine's %s; update this machine: %s", home.Name, home.Build, self, command)
	case updates:
		d.recordHomeDifference(home, false, "")
	}
	return refusal
}

// updateThisMachineCommand is the command that brings this machine to home's build.
func updateThisMachineCommand(home bridge.Build) string {
	if buildinfo.Channel != "source" {
		return fmt.Sprintf("install attn %s from %s", home.Version, outpostbuild.ReleaseURL(buildinfo.ReleaseSource, home.Version))
	}
	if commit, ok := strings.CutPrefix(home.SourceFingerprint, "git:"); ok {
		command := "git checkout " + commit + " && make install"
		if buildinfo.RepositoryPath != "" {
			command = "cd " + buildinfo.RepositoryPath + " && " + command
		}
		return command
	}
	return "make install from the worktree that built " + home.Version
}

func (d *Daemon) homeRow(home bridge.Identity) store.UpdateRunOutpost {
	return store.UpdateRunOutpost{ID: home.MachineID, Name: home.Name, Role: store.UpdateRoleHome, Build: home.Build.String()}
}

func (d *Daemon) appMachineRow() store.UpdateRunOutpost {
	self := d.bridgeIdentity()
	return store.UpdateRunOutpost{ID: d.store.MachineID(), Name: self.Name, Role: store.UpdateRoleAppMachine, Build: self.Build.String()}
}

// recordHomeDifference keeps one finished run that shows both builds while the home refuses this machine's, so
// Settings, the banner and attn update status show it with the home away too. Nothing installs.
func (d *Daemon) recordHomeDifference(home bridge.Identity, behind bool, command string) {
	d.updateRuns.mu.Lock()
	self := d.bridgeIdentity().Build.String()
	if latest := d.latestUpdateRun(); latest != nil && (latest.Open() || latest.InstallKind == installKindHome && latest.TargetBuild == self && runShowsHomeOn(*latest, home.Build.String())) {
		d.updateRuns.mu.Unlock()
		return
	}
	run, err := d.createUpdateRun(installKindHome)
	d.updateRuns.mu.Unlock()
	if err != nil {
		d.logf("record the build difference with home %s: %v", home.Name, err)
		return
	}
	if err := d.store.SetUpdateRunTarget(run.ID, self); err != nil {
		d.logf("update run %s: %v", run.ID, err)
	}
	homeRow := d.homeRow(home)
	homeRow.State = store.UpdateOutpostDone
	d.setUpdateRow(run, homeRow)
	app := d.appMachineRow()
	app.State, app.Action = store.UpdateOutpostSkipped, actionUpdate
	outcome := fmt.Sprintf("Nothing installed: %s runs %s, this machine runs %s.", home.Name, home.Build, self)
	if behind {
		app.State, app.Reason, app.Action = store.UpdateOutpostFailed, string(protocol.UpdateFailureReasonBehind), command
		app.Detail = home.Name + " runs a newer build"
	} else {
		app.Detail = home.Name + " runs another build"
	}
	d.setUpdateRow(run, app)
	d.finishUpdateRun(run, protocol.UpdatePhaseFailed, outcome)
}

func runShowsHomeOn(run store.UpdateRun, build string) bool {
	for _, row := range run.Outposts {
		if row.Role == store.UpdateRoleHome {
			return row.Build == build
		}
	}
	return false
}

func (d *Daemon) createUpdateRun(kind string) (store.UpdateRun, error) {
	logDir := filepath.Join(d.dataRoot, "update-logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return store.UpdateRun{}, fmt.Errorf("create the update log folder %s: %w", logDir, err)
	}
	id := uuid.NewString()
	run, err := d.store.CreateUpdateRun(id, kind, string(protocol.UpdatePhasePreparing), filepath.Join(logDir, id+".log"), time.Now())
	if err != nil {
		return store.UpdateRun{}, err
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
	return run, nil
}

// startManualHomeUpdate is Update on an app machine whose home refuses its build.
func (d *Daemon) startManualHomeUpdate() (*store.UpdateRun, error) {
	status := d.enrollmentStatus()
	r := &d.outpost
	r.mu.Lock()
	home := r.refusedBy
	r.mu.Unlock()
	if home == nil {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "this machine is not refused by its home's build, so there is nothing to push; outposts update from the home")
	}
	d.forgetSharedBuild()
	return d.startHomeUpdate(status.Dial, *home)
}

func (d *Daemon) startHomeUpdate(destination string, home bridge.Identity) (*store.UpdateRun, error) {
	d.updateRuns.mu.Lock()
	defer d.updateRuns.mu.Unlock()
	if run := d.latestUpdateRun(); run != nil && run.Open() {
		return run, nil
	}
	run, err := d.createUpdateRun(installKindHome)
	if err != nil {
		return nil, err
	}
	self := d.bridgeIdentity().Build
	run.TargetBuild = self.String()
	if err := d.store.SetUpdateRunTarget(run.ID, run.TargetBuild); err != nil {
		return nil, err
	}
	app := d.appMachineRow()
	app.State = store.UpdateOutpostDone
	d.setUpdateRow(run, app)
	homeRow := d.homeRow(home)
	homeRow.State, homeRow.Detail = store.UpdateOutpostStaging, "connecting"
	d.setUpdateRow(run, homeRow)
	d.life.Go("updateHome", func() { d.runHomeUpdate(run, destination, homeRow) })
	return d.latestUpdateRun(), nil
}

func (d *Daemon) runHomeUpdate(run store.UpdateRun, destination string, row store.UpdateRunOutpost) {
	ctx := d.life.Context()
	staging, cancel := context.WithTimeout(ctx, selfupdate.BuildTimeoutFromEnv())
	defer cancel()
	d.setUpdatePhase(run, protocol.UpdatePhaseStaging)
	pausepoint.At(pausepoint.HomeUpdateStaging)
	step := func(detail string) {
		d.logf("update run %s: home %s %s", run.ID, row.Name, detail)
		row.State, row.Detail = store.UpdateOutpostStaging, detail
		d.setUpdateRow(run, row)
	}
	remote, binaries, err := d.stageHome(staging, destination, step)
	if err == nil {
		d.setUpdatePhase(run, protocol.UpdatePhaseApplying)
		row.State, row.Detail = store.UpdateOutpostSwitching, ""
		d.setUpdateRow(run, row)
		err = d.switchHome(ctx, remote, binaries)
	}
	if err != nil {
		d.logf("update run %s: home %s failed (%s): %v", run.ID, row.Name, reasonOf(err), err)
		d.setUpdateRow(run, failedRow(row, err))
		d.finishUpdateRun(run, protocol.UpdatePhaseFailed, fmt.Sprintf("%s stays on its build: %v", row.Name, err))
		return
	}
	d.logf("update run %s: home %s is back on %s", run.ID, row.Name, d.bridgeIdentity().Build)
	row.State, row.Detail, row.Build = store.UpdateOutpostDone, "", d.bridgeIdentity().Build.String()
	d.setUpdateRow(run, row)
	d.setUpdatePhase(run, protocol.UpdatePhaseOutposts)
	summary := "attn is now running " + row.Build + " on " + row.Name + "."
	if fleet, err := Call[protocol.UpdateRun](ctx, d.router(), protocol.UpdateFleetMessage{Cmd: protocol.CmdUpdateFleet}); err != nil {
		summary += " Its outposts were not updated: " + err.Error()
	} else {
		summary += " Its outposts update in run " + fleet.ID + "."
	}
	d.finishUpdateRun(run, protocol.UpdatePhaseDone, summary)
}

// stageHome sends this machine's build to its home and has the home check it. Nothing on the home changes.
func (d *Daemon) stageHome(ctx context.Context, destination string, step func(string)) (*outpostssh.Client, outpostbuild.Binaries, error) {
	remote := d.homeSSH(destination)
	if err := remote.EnsureMaster(ctx); err != nil {
		return nil, outpostbuild.Binaries{}, fault(string(protocol.UpdateFailureReasonUnreachable), err)
	}
	probe, err := remote.Probe(ctx)
	if err != nil {
		return nil, outpostbuild.Binaries{}, sshFault(err)
	}
	status := d.enrollmentStatus()
	if !probe.Enrollment.IsHome() || probe.Enrollment.HomeID != status.HomeID {
		return nil, outpostbuild.Binaries{}, fault(string(protocol.UpdateFailureReasonRefused), fmt.Errorf("%s is not the home of this machine (%s); it reports %s", destination, status.HomeID, probe.Enrollment.Describe()))
	}
	self := d.bridgeIdentity().Build
	binaries, err := d.outpostBinaries(ctx, self, probe.Platform, step)
	if err != nil {
		return nil, outpostbuild.Binaries{}, fault(string(protocol.UpdateFailureReasonNoBuild), err)
	}
	if err := remote.Stage(ctx, binaries, self, step); err != nil {
		return nil, outpostbuild.Binaries{}, sshFault(err)
	}
	return remote, binaries, nil
}

// switchHome installs the staged build on the home and redials it. The home restarts on the new build when the
// dial runs the new attn, and its terminals live on in the shared PTY host.
func (d *Daemon) switchHome(ctx context.Context, remote *outpostssh.Client, binaries outpostbuild.Binaries) error {
	self := d.bridgeIdentity()
	bridged := d.homeBridgedSignal()
	if err := remote.Switch(ctx, binaries, self.Build, true, &outpostssh.Pusher{MachineID: d.store.MachineID(), Name: self.Name}); err != nil {
		return sshFault(err)
	}
	pausepoint.At(pausepoint.HomeUpdateSwitched)
	r := &d.outpost
	r.mu.Lock()
	dial := r.dial
	r.mu.Unlock()
	if dial != nil {
		dial.kick()
	}
	wait := stepclock.NewTimer(HomeRejoinTimer, rejoinOnNewBuild)
	defer wait.Stop()
	select {
	case <-bridged:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-wait.C():
		return fault(string(protocol.UpdateFailureReasonNotBack), fmt.Errorf("switched to %s and did not connect on it within %s", self.Build, rejoinOnNewBuild))
	}
}

// answerUpdateStatusAwayFromHome answers attn update status on an app machine whose bridge to its home is down,
// from this machine's own run: the home cannot answer, and this machine's run holds its row and the home's.
func (d *Daemon) answerUpdateStatusAwayFromHome(ctx context.Context, conn net.Conn) bool {
	state, _, _ := d.outpostSettings()
	if d.enrollmentStatus().Dial == "" || state == "bridged" {
		return false
	}
	d.handleUpdateStatus(ctx, conn)
	return true
}

// updateFleet runs at the home once an app machine has brought it to the app machine's build: it brings the
// outposts to that build in the same run, one attempt each.
func (d *Daemon) updateFleet(ctx context.Context, _ protocol.UpdateFleetMessage) (protocol.UpdateRun, error) {
	origin, remote := OriginOf(ctx)
	if !remote || !d.isOutpostConn(origin.Host, origin.bridge) {
		return protocol.UpdateRun{}, errors.New("update_fleet comes from an app machine over its bridge")
	}
	d.updateRuns.mu.Lock()
	if latest := d.latestUpdateRun(); latest != nil && latest.Open() {
		d.updateRuns.mu.Unlock()
		return *updateRunToProtocol(latest), nil
	}
	run, err := d.createUpdateRun(installKindFleet)
	if err != nil {
		d.updateRuns.mu.Unlock()
		return protocol.UpdateRun{}, err
	}
	self := d.bridgeIdentity()
	run.TargetBuild = self.Build.String()
	if err := d.store.SetUpdateRunTarget(run.ID, run.TargetBuild); err != nil {
		d.updateRuns.mu.Unlock()
		return protocol.UpdateRun{}, err
	}
	d.setUpdateRow(run, store.UpdateRunOutpost{ID: d.store.MachineID(), Name: self.Name, Role: store.UpdateRoleHome, State: store.UpdateOutpostDone, Build: run.TargetBuild, Detail: "updated from " + origin.Name})
	d.setUpdateRow(run, store.UpdateRunOutpost{ID: origin.Host, Name: origin.Name, Role: store.UpdateRoleAppMachine, State: store.UpdateOutpostDone, Build: run.TargetBuild})
	d.updateRuns.mu.Unlock()
	d.life.Go("updateFleet", func() {
		staging, cancel := context.WithTimeout(d.life.Context(), selfupdate.BuildTimeoutFromEnv())
		defer cancel()
		d.setUpdatePhase(run, protocol.UpdatePhaseStaging)
		d.stageOutposts(staging, run, self.Build, nil, origin.Host)
		d.setUpdatePhase(run, protocol.UpdatePhaseOutposts)
		summary := d.switchOutposts(run)
		d.finishUpdateRun(run, protocol.UpdatePhaseDone, "attn is now running "+run.TargetBuild+" on "+self.Name+"."+summary)
	})
	return *updateRunToProtocol(d.latestUpdateRun()), nil
}

// supplyOutpostBuild runs on the app machine that holds its build's source: it sends the build's binaries for an
// outpost platform into its home's cache.
func (d *Daemon) supplyOutpostBuild(ctx context.Context, msg protocol.SupplyOutpostBuildMessage) (struct{}, error) {
	status := d.enrollmentStatus()
	if !status.IsOutpost() || status.Dial == "" {
		return struct{}{}, errors.New("this machine does not dial its home, so it cannot send builds to it")
	}
	self := d.bridgeIdentity().Build
	binaries, err := d.outpostBinaries(ctx, self, msg.Platform, func(string) {})
	if err != nil {
		return struct{}{}, err
	}
	return struct{}{}, d.homeSSH(status.Dial).PutCache(ctx, binaries, self, msg.Platform)
}

// sourceBinaries asks the machine holding the source of this home's build for an outpost platform's binaries.
func (d *Daemon) sourceBinaries(ctx context.Context, cache string, source outpostbuild.Source, platform string, phase func(string)) (outpostbuild.Binaries, error) {
	if cached, ok := outpostbuild.Cached(cache, source.Build, platform); ok {
		return cached, nil
	}
	phase("building")
	if _, err := Call[struct{}](ctx, d.router(), protocol.SupplyOutpostBuildMessage{Cmd: protocol.CmdSupplyOutpostBuild, Host: source.MachineID, Platform: platform}); err != nil {
		return outpostbuild.Binaries{}, fault(string(protocol.UpdateFailureReasonNoBuild), fmt.Errorf("%s holds the source of this build and could not send its %s binaries: %v", source.Name, platform, err))
	}
	cached, ok := outpostbuild.Cached(cache, source.Build, platform)
	if !ok {
		return outpostbuild.Binaries{}, fault(string(protocol.UpdateFailureReasonNoBuild), fmt.Errorf("%s sent an incomplete %s build", source.Name, platform))
	}
	return cached, nil
}
