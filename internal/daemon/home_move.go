package daemon

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/homemove"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

const (
	homeMoveCheckChannel  = "home-move-check"
	homeMoveStageChannel  = "home-move-stage"
	homeMoveCommitChannel = "home-move-commit"
	homeMoveAbortChannel  = "home-move-abort"
)

const (
	stepChecked  = "checked"
	stepChecking = "checking"
	stepRefused  = "refused"
)

type homeMoveRuntime struct {
	mu   sync.Mutex
	held bool
	lift func()
	// yielded is a home that rolled a move back and runs no home role until the target is its outpost again, so
	// two machines never run the home at once.
	yielded bool
	// checking is the move whose checks run before it writes its record.
	checking *protocol.HomeMove
	// run is the move this machine runs now, or the last it ran since it started.
	run *homeMoveRun
}

type homeMoveFolder struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Profiles []string `json:"profiles"`
}

type homeMoveCheckRequest struct {
	SSH     []string         `json:"ssh"`
	Folders []homeMoveFolder `json:"folders"`
}

type homeMoveCheckAnswer struct {
	Checks []protocol.HomeMoveCheck `json:"checks"`
	Dial   string                   `json:"dial,omitempty"`
	Remaps []store.PathRemap        `json:"remaps"`
	Sleeps bool                     `json:"sleeps,omitempty"`
}

type homeMoveStage struct {
	Record homemove.Record `json:"record"`
	Home   store.MovedHome `json:"home"`
}

type homeMoveStaged struct {
	Error string `json:"error,omitempty"`
}

type homeMoveRequest struct {
	ID string `json:"id"`
}

type homeMovePlan struct {
	target    store.Machine
	conn      *bridge.Conn
	transport string
	dial      string
	remaps    []store.PathRemap
	checks    []protocol.HomeMoveCheck
	sleeps    bool
}

// exitForMove ends this daemon so the next start applies or rolls back the move recorded on disk. Whatever started it
// starts it again: the systemd unit, the app, or the bridge that dials it.
var exitForMove = func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }

func (d *Daemon) runsHomeRole() bool {
	return d.enrollmentStatus().IsHome() && d.homeMoveIdle() == ""
}

// homeMoveRun is one move command, and how it ended once ended closes.
type homeMoveRun struct {
	ended   chan struct{}
	outcome protocol.HomeMove
	err     error
}

// homeMoveIdle says why a move keeps this home from running its role, or is empty when none does.
func (d *Daemon) homeMoveIdle() string {
	d.homeMove.mu.Lock()
	defer d.homeMove.mu.Unlock()
	switch {
	case d.homeMove.held:
		return "this home is moving to another machine and holds everything until the move ends"
	case d.homeMove.yielded:
		return "this home rolled a move back and waits until the machine it moved to is its outpost again; roll the move back again to stop waiting"
	}
	return ""
}

// holdForMove fences this home until the move ends: its database refuses every write, so nothing is written after
// the snapshot the target holds, and its cloud link and home jobs stop.
func (d *Daemon) holdForMove() {
	d.homeMove.mu.Lock()
	if d.homeMove.held {
		d.homeMove.mu.Unlock()
		return
	}
	d.homeMove.held = true
	d.homeMove.lift = d.store.Fence()
	d.homeMove.mu.Unlock()
	d.configurePhoneAccess()
	d.refreshJobQueueRole()
}

// fenceHeldHome fences a home that restarted while its move was committing, once start-up recovery has written what
// it needs. Until then it takes writes as any starting daemon does.
func (d *Daemon) fenceHeldHome() {
	d.homeMove.mu.Lock()
	defer d.homeMove.mu.Unlock()
	if d.homeMove.held && d.homeMove.lift == nil {
		d.homeMove.lift = d.store.Fence()
	}
}

func (d *Daemon) releaseMoveHold() {
	d.homeMove.mu.Lock()
	lift := d.homeMove.lift
	d.homeMove.held, d.homeMove.lift = false, nil
	d.homeMove.mu.Unlock()
	if lift != nil {
		lift()
	}
	d.configurePhoneAccess()
	d.refreshJobQueueRole()
}

// settleHomeMoveAtStart runs before the store opens: a committed move is applied, a requested rollback is done.
func (d *Daemon) settleHomeMoveAtStart() error {
	if err := rebuildMovedHome(d.dataRoot); err != nil {
		return err
	}
	record, applied, err := homemove.Apply(d.dataRoot, config.DBPath())
	if err != nil {
		return fmt.Errorf("apply move %s: %w", record.ID, err)
	}
	if applied {
		if record.Side == homemove.SideTo {
			_ = os.Remove(homemove.Snapshot(d.dataRoot, record.ID))
		}
		if record.Side == homemove.SideFrom {
			if err := keepNotebookAsCopy(d.dataRoot); err != nil {
				return fmt.Errorf("keep the Notebook in place after move %s: %w", record.ID, err)
			}
		}
		d.logf("home move %s: applied on this machine (%s)", record.ID, record.Side)
	}
	if record.Step == homemove.StepRollingBack {
		if _, err := homemove.Rollback(d.dataRoot, config.DBPath(), record.ID); err != nil {
			return fmt.Errorf("roll back move %s: %w", record.ID, err)
		}
		d.logf("home move %s: rolled back on this machine", record.ID)
	}
	return nil
}

// rebuildMovedHome builds the new home's database again from the staged snapshot and this machine's database as it
// is now, before a committed move swaps it in, so nothing this machine wrote after it staged the move is lost.
func rebuildMovedHome(dataRoot string) error {
	record, found, err := homemove.Latest(dataRoot)
	if err != nil || !found || record.Side != homemove.SideTo || record.Step != homemove.StepCommitted {
		return err
	}
	snapshot, staged := homemove.Snapshot(dataRoot, record.ID), homemove.StagedDatabase(dataRoot, record.ID)
	if !fileExists(snapshot) || !fileExists(staged) {
		return nil
	}
	moved, err := readMovedHome(dataRoot, record.ID)
	if err != nil {
		return err
	}
	own := config.DBPath()
	if kept := homemove.PreMoveDatabase(dataRoot, record.ID); fileExists(kept) {
		own = kept
	}
	rebuilt := staged + ".rebuilt"
	if err := store.BuildMovedHome(context.Background(), moved, snapshot, own, rebuilt); err != nil {
		return fmt.Errorf("build the new home's database for move %s: %w", record.ID, err)
	}
	return os.Rename(rebuilt, staged)
}

func movedHomePath(dataRoot, id string) string {
	return filepath.Join(homemove.Dir(dataRoot, id), "moved-home.json")
}

func readMovedHome(dataRoot, id string) (store.MovedHome, error) {
	var moved store.MovedHome
	data, err := os.ReadFile(movedHomePath(dataRoot, id))
	if err != nil {
		return moved, err
	}
	return moved, json.Unmarshal(data, &moved)
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// resumeHomeMoveAtStart forgets a move this home staged but never began to commit, and holds a home whose move is
// committing until the move is finished or rolled back. The target keeps what it staged: the home may still commit it.
func (d *Daemon) resumeHomeMoveAtStart() {
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil {
		d.logf("home move: %v", err)
		return
	}
	switch {
	case !found:
	case record.Side == homemove.SideFrom && record.Step == homemove.StepDone, record.Side == homemove.SideTo && record.Step == homemove.StepRolledBack:
		if err := d.removeHomeUnit(d.life.Context()); err != nil {
			d.logf("home move %s: remove the systemd unit that started the home: %v", record.ID, err)
		}
	case record.Side == homemove.SideTo && record.Step == homemove.StepApplied:
		if err := d.keepHomeUnit(d.life.Context()); err != nil {
			d.logf("home move %s: install the systemd unit that starts the home: %v", record.ID, err)
		}
	case record.Side == homemove.SideFrom && record.Step == homemove.StepRolledBack && !record.TargetReturned:
		d.logf("home move %s: rolled back; running no home role until %s is an outpost again", record.ID, record.To.Name)
		d.homeMove.mu.Lock()
		d.homeMove.yielded = true
		d.homeMove.mu.Unlock()
	case record.Side == homemove.SideFrom && record.Step == homemove.StepStaged:
		if err := homemove.Abort(d.dataRoot, record.ID); err != nil {
			d.logf("home move %s: forget the staged move: %v", record.ID, err)
		}
	case record.Side == homemove.SideFrom && record.Step == homemove.StepCommitting:
		d.logf("home move %s: committing to %s; holding until attn home move or attn home rollback", record.ID, record.To.Name)
		d.homeMove.mu.Lock()
		d.homeMove.held = true
		d.homeMove.mu.Unlock()
	}
}

func (d *Daemon) bridgeMoveState() *bridge.MoveState {
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil || !found || record.Side != homemove.SideFrom {
		return nil
	}
	switch record.Step {
	case homemove.StepCommitting, homemove.StepRolledBack:
		return &bridge.MoveState{ID: record.ID, Step: string(record.Step)}
	}
	return nil
}

func moveInfo(record homemove.Record) protocol.HomeMove {
	info := protocol.HomeMove{ID: protocol.Ptr(record.ID), Side: protocol.Ptr(string(record.Side)), Step: string(record.Step), From: protocol.Ptr(record.From.Name), To: protocol.Ptr(record.To.Name), Checks: []protocol.HomeMoveCheck{}, Remaps: []protocol.HomeMoveRemap{}}
	if record.Error != "" {
		info.Error = protocol.Ptr(record.Error)
	}
	if record.Side == homemove.SideFrom && record.Step == homemove.StepRolledBack && !record.TargetReturned {
		info.WaitsForTarget = protocol.Ptr(true)
	}
	for _, remap := range record.Notebook {
		info.Remaps = append(info.Remaps, protocol.HomeMoveRemap{From: remap.From, To: remap.To})
	}
	return info
}

func (d *Daemon) handleHomeMove(ctx context.Context, conn net.Conn, msg *protocol.HomeMoveMessage) {
	move, exit, err := d.homeMoveCommand(ctx, msg, func(string, string) {})
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, HomeMove: &move})
	if exit {
		_ = conn.Close()
		exitForMove()
	}
}

func (d *Daemon) handleHomeMoveStatus(conn net.Conn) {
	move, err := d.homeMoveStatus()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, HomeMove: &move})
}

func homeMoveResult(requestID string, move protocol.HomeMove, err error) protocol.HomeMoveResultMessage {
	result := protocol.HomeMoveResultMessage{Event: protocol.EventHomeMoveResult, RequestID: requestID, Success: err == nil}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.HomeMove = &move
	}
	return result
}

// homeMoveStatusOnceEnded answers with how the move this machine runs, or ran last, ended, as the move command
// answered, so a window that lost the answer of the move it started still hears it. With no such move since this
// daemon started, it answers the status.
func (d *Daemon) homeMoveStatusOnceEnded(ctx context.Context) (protocol.HomeMove, error) {
	d.homeMove.mu.Lock()
	run := d.homeMove.run
	d.homeMove.mu.Unlock()
	if run == nil {
		return d.homeMoveStatus()
	}
	select {
	case <-run.ended:
		return run.outcome, run.err
	case <-ctx.Done():
		return protocol.HomeMove{}, ctx.Err()
	}
}

func (d *Daemon) homeMoveStatus() (protocol.HomeMove, error) {
	record, found, err := homemove.Latest(d.dataRoot)
	d.homeMove.mu.Lock()
	checking := d.homeMove.checking
	d.homeMove.mu.Unlock()
	if err == nil && checking != nil && (!found || record.ID != protocol.Deref(checking.ID)) {
		return *checking, nil
	}
	if err != nil || !found {
		return protocol.HomeMove{Step: "none", Checks: []protocol.HomeMoveCheck{}, Remaps: []protocol.HomeMoveRemap{}}, err
	}
	return moveInfo(record), nil
}

// handleHomeMoveWS answers a move command from the app with home_move_result. A daemon the command ends exits once it
// has written the answer.
func (d *Daemon) handleHomeMoveWS(client *wsClient, requestID string, run func(context.Context) (protocol.HomeMove, bool, error)) {
	d.life.Go("homeMoveWS", func() {
		move, exit, err := run(d.life.Context())
		d.sendToClient(client, homeMoveResult(requestID, move, err))
		if exit {
			client.closeSendChannel()
			<-client.written
			exitForMove()
		}
	})
}

func (d *Daemon) handleHomeMoveRollback(ctx context.Context, conn net.Conn) {
	move, exit, err := d.rollbackHomeMove(ctx)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, HomeMove: &move})
	if exit {
		_ = conn.Close()
		exitForMove()
	}
}

func (d *Daemon) homeMoveCommand(ctx context.Context, msg *protocol.HomeMoveMessage, began func(id, step string)) (move protocol.HomeMove, exit bool, err error) {
	if !d.on(setting.RemoteHomeEnabled) {
		return protocol.HomeMove{}, false, protocol.Refuse(protocol.RefusalWrongState, "remote_home_enabled is off; run {attn settings set remote_home_enabled true} before you move the home")
	}
	status := d.enrollmentStatus()
	if !status.IsHome() {
		return protocol.HomeMove{}, false, protocol.Refuse(protocol.RefusalWrongState, "this machine is an outpost of home %s; run {attn home move} on the machine that is the home", status.HomeID)
	}
	place, err := d.readMachine(msg.Machine)
	if err != nil {
		return protocol.HomeMove{}, false, err
	}
	if place.IsLocal() {
		return protocol.HomeMove{}, false, protocol.Refuse(protocol.RefusalInvalidInput, "%s names this machine, which is already the home; name an outpost", msg.Machine)
	}
	target, err := d.machineByID(place.ID())
	if err != nil {
		return protocol.HomeMove{}, false, err
	}
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil {
		return protocol.HomeMove{}, false, err
	}
	if found && record.Side == homemove.SideFrom && record.Step == homemove.StepCommitting {
		if record.To.ID != target.ID {
			return protocol.HomeMove{}, false, protocol.Refuse(protocol.RefusalWrongState, "move %s to %s is committing; run {attn home move %s} to finish it, or {attn home rollback}", record.ID, record.To.Name, record.To.Name)
		}
		if protocol.Deref(msg.Check) {
			return moveInfo(record), false, nil
		}
		return d.finishHomeMoveCommit(ctx, record)
	}
	var id string
	if !protocol.Deref(msg.Check) {
		if id, err = newMoveID(); err != nil {
			return protocol.HomeMove{}, false, err
		}
		run := &homeMoveRun{ended: make(chan struct{})}
		d.homeMove.mu.Lock()
		d.homeMove.run = run
		d.homeMove.checking = &protocol.HomeMove{ID: protocol.Ptr(id), Side: protocol.Ptr(string(homemove.SideFrom)), Step: stepChecking, From: protocol.Ptr(d.bridgeIdentity().Name), To: protocol.Ptr(target.Name), Checks: []protocol.HomeMoveCheck{}, Remaps: []protocol.HomeMoveRemap{}}
		d.homeMove.mu.Unlock()
		defer func() {
			run.outcome, run.err = move, err
			if run.outcome.ID == nil {
				run.outcome.ID = protocol.Ptr(id)
			}
			d.homeMove.mu.Lock()
			if d.homeMove.checking != nil && protocol.Deref(d.homeMove.checking.ID) == id {
				d.homeMove.checking = nil
			}
			d.homeMove.mu.Unlock()
			close(run.ended)
		}()
	}
	if id != "" {
		pausepoint.At(pausepoint.HomeMoveChecking)
	}
	plan := d.checkHomeMove(ctx, target, strings.TrimSpace(protocol.Deref(msg.Via)))
	d.life.Go("settleMoveCheckTerminals", func() { d.settleMoveCheckTerminals(target.ID, plan.checks) })
	info := protocol.HomeMove{Step: stepChecked, From: protocol.Ptr(d.bridgeIdentity().Name), To: protocol.Ptr(target.Name), Checks: plan.checks, Remaps: []protocol.HomeMoveRemap{}}
	if plan.sleeps {
		info.Sleeps = protocol.Ptr(true)
	}
	for _, remap := range plan.remaps {
		info.Remaps = append(info.Remaps, protocol.HomeMoveRemap{From: remap.From, To: remap.To})
	}
	if slices.ContainsFunc(plan.checks, func(c protocol.HomeMoveCheck) bool { return !c.Ok }) {
		info.Step = stepRefused
		return info, false, nil
	}
	if protocol.Deref(msg.Check) {
		return info, false, nil
	}
	return d.runHomeMove(ctx, id, plan, info, began)
}

func check(name string, err error) protocol.HomeMoveCheck {
	if err != nil {
		return protocol.HomeMoveCheck{Name: name, Ok: false, Detail: protocol.Ptr(err.Error())}
	}
	return protocol.HomeMoveCheck{Name: name, Ok: true}
}

func fixableCheck(name string, err error, fix string) protocol.HomeMoveCheck {
	checked := check(name, err)
	if err != nil {
		checked.Fix = protocol.Ptr(fix)
	}
	return checked
}

func (d *Daemon) checkHomeMove(ctx context.Context, target store.Machine, via string) homeMovePlan {
	plan := homeMovePlan{target: target}
	conn, name, err := d.outpostConn(target.ID)
	plan.checks = append(plan.checks, check("connected", err))
	if err != nil {
		return plan
	}
	plan.conn = conn
	self := d.bridgeIdentity()
	var buildErr error
	if peer := conn.Peer().Build; peer != self.Build {
		buildErr = fmt.Errorf("%s runs build %s; this home runs %s", name, peer, self.Build)
	}
	plan.checks = append(plan.checks, check("build", buildErr))
	var notebookErr error
	if state := d.notebookPeerState(conn.Peer().DaemonID, name); state.State != protocol.OutpostNotebookStateInSync {
		notebookErr = fmt.Errorf("the Notebook on %s is %s; wait until it is in sync", name, state.State)
	}
	plan.checks = append(plan.checks, check("notebook", notebookErr))
	request := homeMoveCheckRequest{SSH: []string{}, Folders: []homeMoveFolder{}}
	rows, err := d.store.ListOutposts()
	if err != nil {
		plan.checks = append(plan.checks, check("outposts", err))
		return plan
	}
	for _, row := range rows {
		if row.ID != target.ID && !row.Reverse() {
			request.SSH = append(request.SSH, row.Destination)
		}
	}
	folders, err := d.notebookFolders()
	if err != nil {
		plan.checks = append(plan.checks, check("notebook", err))
		return plan
	}
	for _, folder := range folders {
		request.Folders = append(request.Folders, homeMoveFolder{ID: folder.ID, Path: folder.HomePath, Profiles: folder.Profiles})
	}
	switch {
	case target.Reverse(), via != "":
		plan.transport = "ssh"
	case runtime.GOOS == "darwin":
		plan.transport, plan.dial = store.TransportReverse, target.Destination
	default:
		plan.checks = append(plan.checks, check("reach back", fmt.Errorf("%s keeps this machine updated over ssh; give --via with the ssh destination by which %s reaches this machine", name, name)))
		return plan
	}
	if plan.transport == "ssh" && via != "" {
		if err := validateOutpostDestination(via); err != nil {
			plan.checks = append(plan.checks, check("reach back", err))
			return plan
		}
		plan.dial = via
		request.SSH = append(request.SSH, via)
	}
	var answer homeMoveCheckAnswer
	if err := conn.Call(ctx, homeMoveCheckChannel, request, &answer); err != nil {
		plan.checks = append(plan.checks, check("target", err))
		return plan
	}
	plan.checks = append(plan.checks, answer.Checks...)
	if plan.transport == "ssh" && plan.dial == "" {
		plan.dial = answer.Dial
		var reachErr error
		if plan.dial == "" {
			reachErr = fmt.Errorf("%s dials this home without an ssh destination; give --via with the ssh destination by which %s reaches this machine", name, name)
		}
		plan.checks = append(plan.checks, check("reach back", reachErr))
	}
	plan.remaps = answer.Remaps
	plan.sleeps = answer.Sleeps
	return plan
}

func newMoveID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "m-" + hex.EncodeToString(buf), nil
}

func (d *Daemon) runHomeMove(ctx context.Context, id string, plan homeMovePlan, info protocol.HomeMove, began func(id, step string)) (protocol.HomeMove, bool, error) {
	status, self := d.enrollmentStatus(), d.bridgeIdentity()
	peer := plan.conn.Peer()
	from := homemove.Machine{ID: d.store.MachineID(), Name: self.Name, DaemonID: d.daemonInstanceID}
	to := homemove.Machine{ID: plan.target.ID, Name: plan.target.Name, DaemonID: peer.DaemonID}
	roots := homemove.MovingRoots
	record := homemove.Record{ID: id, Side: homemove.SideFrom, HomeID: status.HomeID, From: from, To: to, Step: homemove.StepStaged, Roots: roots,
		Enrollment: enrollment.Record{Role: enrollment.RoleOutpost, HomeID: status.HomeID, HomeName: peer.Name, HomeDaemonID: peer.DaemonID, MachineID: from.ID, HomeMachineID: to.ID}}
	if plan.transport == store.TransportReverse {
		record.Enrollment.Dial = plan.dial
	}
	d.holdForMove()
	fail := func(err error) (protocol.HomeMove, bool, error) {
		_ = plan.conn.Call(context.WithoutCancel(ctx), homeMoveAbortChannel, homeMoveRequest{ID: id}, nil)
		if abortErr := homemove.Abort(d.dataRoot, id); abortErr != nil {
			d.logf("home move %s: forget the staged move: %v", id, abortErr)
		}
		d.releaseMoveHold()
		info.ID, info.Step, info.Error = protocol.Ptr(id), string(homemove.StepAborted), protocol.Ptr(err.Error())
		d.logf("home move %s: aborted: %v", id, err)
		return info, false, nil
	}
	if err := homemove.Write(d.dataRoot, record); err != nil {
		return fail(err)
	}
	began(id, "fence")
	crashpoint.At("home-move-fenced")
	snapshot := filepath.Join(homemove.Dir(d.dataRoot, id), "snapshot.db")
	if err := d.store.Snapshot(snapshot); err != nil {
		return fail(fmt.Errorf("snapshot the home's database: %w", err))
	}
	files, err := listMovingFiles(d.dataRoot, roots)
	if err != nil {
		return fail(err)
	}
	if err := store.BuildMovedOutpost(ctx, snapshot, homemove.StagedDatabase(d.dataRoot, id), status.HomeID, from.ID); err != nil {
		return fail(fmt.Errorf("build this machine's database as an outpost: %w", err))
	}
	hostKey, err := bridge.PublicHostKey(filepath.Join(d.dataRoot, "bridge_host_key"))
	if err != nil {
		return fail(err)
	}
	stage := homeMoveStage{
		Record: homemove.Record{ID: id, Side: homemove.SideTo, HomeID: status.HomeID, From: from, To: to, Step: homemove.StepStaged, Roots: roots, Files: files,
			Enrollment: enrollment.Record{Role: enrollment.RoleHome, HomeID: status.HomeID, HomeDaemonID: peer.DaemonID, MachineID: to.ID}},
		Home: store.MovedHome{Home: from.ID, Target: to.ID, HomeTransport: plan.transport, HomeHostKey: hostKey, HomeDaemonID: from.DaemonID, Remaps: plan.remaps},
	}
	if plan.transport == "ssh" {
		stage.Home.HomeDial = plan.dial
	}
	for _, remap := range plan.remaps {
		stage.Record.Notebook = append(stage.Record.Notebook, homemove.Remap{From: remap.From, To: remap.To})
	}
	record.Notebook = stage.Record.Notebook
	began(id, "copy")
	if err := d.sendHomeMoveStage(ctx, plan.conn, stage, snapshot, files, func() { began(id, "build") }); err != nil {
		return fail(fmt.Errorf("stage the move on %s: %w", to.Name, err))
	}
	_ = os.Remove(snapshot)
	crashpoint.At("home-move-staged")
	record.Step = homemove.StepCommitting
	if err := homemove.Write(d.dataRoot, record); err != nil {
		return fail(err)
	}
	d.logf("home move %s: staged on %s; committing", id, to.Name)
	began(id, "commit")
	return d.commitHomeMove(ctx, plan.conn, record)
}

func (d *Daemon) commitHomeMove(ctx context.Context, conn *bridge.Conn, record homemove.Record) (protocol.HomeMove, bool, error) {
	crashpoint.At("home-move-committing")
	if err := conn.Call(ctx, homeMoveCommitChannel, homeMoveRequest{ID: record.ID}, nil); err != nil {
		info := moveInfo(record)
		info.Error = protocol.Ptr(fmt.Sprintf("%s did not confirm the commit: %v; run attn home move %s again to finish, or attn home rollback", record.To.Name, err, record.To.Name))
		return info, false, nil
	}
	return d.finishOwnCommit(record)
}

func (d *Daemon) finishOwnCommit(record homemove.Record) (protocol.HomeMove, bool, error) {
	crashpoint.At("home-move-target-confirmed")
	record.Step = homemove.StepCommitted
	if err := homemove.Write(d.dataRoot, record); err != nil {
		return moveInfo(record), false, err
	}
	d.logf("home move %s: committed; restarting as an outpost of %s", record.ID, record.To.Name)
	return moveInfo(record), true, nil
}

// finishHomeMoveCommit finishes a move whose commit may not have reached the target. A target that still bridges
// commits again by the move's id; a target that refused this home's bridge because it holds the home since this
// move has committed.
func (d *Daemon) finishHomeMoveCommit(ctx context.Context, record homemove.Record) (protocol.HomeMove, bool, error) {
	if conn, _, err := d.outpostConn(record.To.ID); err == nil {
		return d.commitHomeMove(ctx, conn, record)
	}
	if d.targetHoldsMovedHome(record) {
		return d.finishOwnCommit(record)
	}
	info := moveInfo(record)
	info.Error = protocol.Ptr(fmt.Sprintf("%s is unreachable, so this home cannot tell whether it committed; run attn home move %s again once it is reachable, or attn home rollback", record.To.Name, record.To.Name))
	return info, false, nil
}

func (d *Daemon) targetHoldsMovedHome(record homemove.Record) bool {
	r := &d.outposts
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.dialers[record.To.ID]
	if run == nil {
		return false
	}
	var refusal *bridge.Refusal
	return errors.As(run.lastError, &refusal) && refusal.Reason == "moved" && strings.Contains(refusal.Detail, record.ID)
}

func (d *Daemon) rollbackHomeMove(ctx context.Context) (protocol.HomeMove, bool, error) {
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil {
		return protocol.HomeMove{}, false, err
	}
	if !found {
		return protocol.HomeMove{}, false, protocol.Refuse(protocol.RefusalWrongState, "this machine has no move to roll back")
	}
	if record.Side == homemove.SideTo && record.Step != homemove.StepStaged {
		return moveInfo(record), false, protocol.Refuse(protocol.RefusalWrongState, "this machine took the home from %s in move %s and runs it; to give the home back, run {attn home move %s} here", record.From.Name, record.ID, record.From.Name)
	}
	switch record.Step {
	case homemove.StepStaged:
		if err := homemove.Abort(d.dataRoot, record.ID); err != nil {
			return moveInfo(record), false, err
		}
		record.Step = homemove.StepAborted
		return moveInfo(record), false, nil
	case homemove.StepCommitting:
		conn, _, err := d.outpostConn(record.To.ID)
		if err != nil {
			return moveInfo(record), false, fmt.Errorf("%s is unreachable, so this home cannot tell whether it committed: %w", record.To.Name, err)
		}
		if err := conn.Call(ctx, homeMoveAbortChannel, homeMoveRequest{ID: record.ID}, nil); err != nil {
			return moveInfo(record), false, protocol.Refuse(protocol.RefusalWrongState, "%s did not abort its side: %v; run {attn home move %s} to finish the move", record.To.Name, err, record.To.Name)
		}
		if err := homemove.Abort(d.dataRoot, record.ID); err != nil {
			record.Step = homemove.StepAborted
			if err := homemove.Write(d.dataRoot, record); err != nil {
				return moveInfo(record), false, err
			}
		}
		d.releaseMoveHold()
		record.Step = homemove.StepAborted
		return moveInfo(record), false, nil
	case homemove.StepRolledBack:
		if record.Side == homemove.SideFrom && !record.TargetReturned {
			record.TargetReturned = true
			if err := homemove.Write(d.dataRoot, record); err != nil {
				return moveInfo(record), false, err
			}
			d.logf("home move %s: running the home again without word from %s", record.ID, record.To.Name)
			d.endHomeYield()
		}
		return moveInfo(record), false, nil
	case homemove.StepDone:
		return moveInfo(record), false, protocol.Refuse(protocol.RefusalWrongState, "move %s is done: %s runs the home and this machine has bridged to it; to bring the home back, run {attn home move %s} on %s", record.ID, record.To.Name, record.From.Name, record.To.Name)
	case homemove.StepCommitted, homemove.StepApplied:
		record.Step = homemove.StepRollingBack
		if err := homemove.Write(d.dataRoot, record); err != nil {
			return moveInfo(record), false, err
		}
		d.logf("home move %s: rolling back; restarting", record.ID)
		return moveInfo(record), true, nil
	}
	return moveInfo(record), false, nil
}

func listMovingFiles(dataRoot string, roots []string) ([]homemove.File, error) {
	if err := homemove.CheckRoots(roots); err != nil {
		return nil, err
	}
	var files []homemove.File
	for _, root := range roots {
		if !fileExists(filepath.Join(dataRoot, root)) {
			continue
		}
		err := filepath.WalkDir(filepath.Join(dataRoot, root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dataRoot, path)
			if err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				files = append(files, homemove.File{Path: filepath.ToSlash(rel), Link: link})
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			sum, size, err := hashFile(path)
			if err != nil {
				return err
			}
			files = append(files, homemove.File{Path: filepath.ToSlash(rel), SHA256: sum, Size: size, Mode: uint32(info.Mode().Perm())})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (d *Daemon) sendHomeMoveStage(ctx context.Context, conn *bridge.Conn, stage homeMoveStage, snapshot string, files []homemove.File, sent func()) error {
	header, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	channel, requests, err := conn.Open(ctx, homeMoveStageChannel, nil)
	if err != nil {
		return err
	}
	stream := conn.Bulk(ctx, channel, requests)
	defer stream.Close()
	writer := bufio.NewWriter(stream)
	if _, err := writer.Write(append(header, '\n')); err != nil {
		return err
	}
	archive := tar.NewWriter(writer)
	if err := addToArchive(archive, "attn.db", snapshot, homemove.File{Mode: 0o600}); err != nil {
		return err
	}
	for _, file := range files {
		if err := addToArchive(archive, "files/"+file.Path, filepath.Join(d.dataRoot, filepath.FromSlash(file.Path)), file); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	sent()
	var staged homeMoveStaged
	if err := json.NewDecoder(stream).Decode(&staged); err != nil {
		return fmt.Errorf("read the outcome: %w", err)
	}
	if staged.Error != "" {
		return errors.New(staged.Error)
	}
	return nil
}

func addToArchive(archive *tar.Writer, name, path string, file homemove.File) error {
	if file.Link != "" {
		return archive.WriteHeader(&tar.Header{Name: name, Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: file.Link})
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: int64(file.Mode), Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(archive, f)
	return err
}

func (d *Daemon) homeMoveHandlers() map[string]func(*bridge.Conn, ssh.NewChannel) {
	answer := func(name string, handle func(context.Context, *bridge.Conn, []byte) (any, error)) func(*bridge.Conn, ssh.NewChannel) {
		return func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go(name, func() {
				bridge.Answer(incoming, func(ctx context.Context, request []byte) (any, error) { return handle(ctx, conn, request) })
			}) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		}
	}
	return map[string]func(*bridge.Conn, ssh.NewChannel){
		homeMoveCheckChannel: answer("homeMoveCheck", func(ctx context.Context, _ *bridge.Conn, raw []byte) (any, error) {
			var request homeMoveCheckRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return d.answerHomeMoveCheck(ctx, request), nil
		}),
		homeMoveStageChannel: func(conn *bridge.Conn, incoming ssh.NewChannel) {
			if !d.life.Go("homeMoveStage", func() { d.receiveHomeMoveStage(conn, incoming) }) {
				_ = incoming.Reject(ssh.ResourceShortage, "daemon is stopping")
			}
		},
		homeMoveCommitChannel: answer("homeMoveCommit", func(_ context.Context, conn *bridge.Conn, raw []byte) (any, error) {
			var request homeMoveRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			if err := d.commitStagedHome(request.ID); err != nil {
				return nil, err
			}
			d.life.Go("homeMoveRestart", func() {
				select {
				case <-conn.Done():
					exitForMove()
				case <-d.life.Done():
				}
			})
			return nil, nil
		}),
		homeMoveAbortChannel: answer("homeMoveAbort", func(_ context.Context, _ *bridge.Conn, raw []byte) (any, error) {
			var request homeMoveRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return nil, homemove.Abort(d.dataRoot, request.ID)
		}),
	}
}

func (d *Daemon) answerHomeMoveCheck(ctx context.Context, request homeMoveCheckRequest) homeMoveCheckAnswer {
	answer := homeMoveCheckAnswer{Dial: d.enrollmentStatus().Dial, Remaps: []store.PathRemap{}, Sleeps: runtime.GOOS == "darwin"}
	name := d.bridgeIdentity().Name
	for _, destination := range request.SSH {
		client := outpostssh.For(destination, outpostssh.ControlPath(config.InstanceLabel(), "move-"+shortHash(destination)), config.InstanceLabel())
		err := client.EnsureMaster(ctx)
		if err == nil {
			_, err = client.Run(ctx, "true")
			_ = client.ExitMaster(context.WithoutCancel(ctx))
		}
		if err != nil {
			err = fmt.Errorf("%s cannot reach %s with its own ssh: %v", name, destination, err)
		}
		answer.Checks = append(answer.Checks, check("ssh "+destination, err))
	}
	answer.Checks = append(answer.Checks, d.ghCheck(ctx))
	if runtime.GOOS == "linux" {
		answer.Checks = append(answer.Checks, d.lingerCheck(ctx), check("systemd", d.runOnThisMachine(ctx, "systemctl", "--user", "show-environment")))
	}
	var copyErrs []string
	for _, folder := range request.Folders {
		root, err := d.outpostNotebookCopy(folder.ID)
		if err != nil {
			copyErrs = append(copyErrs, err.Error())
			continue
		}
		answer.Remaps = append(answer.Remaps, store.PathRemap{From: folder.Path, To: root, Profiles: folder.Profiles})
	}
	var copyErr error
	if len(copyErrs) > 0 {
		copyErr = errors.New(strings.Join(copyErrs, "; "))
	}
	answer.Checks = append(answer.Checks, check("notebook copy", copyErr))
	return answer
}

func shortHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4])
}

func (d *Daemon) runOnThisMachine(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if env := d.loginEnv.Latest(ctx); len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *Daemon) ghCheck(ctx context.Context) protocol.HomeMoveCheck {
	err := d.runOnThisMachine(ctx, "gh", "auth", "status")
	if errors.Is(err, exec.ErrNotFound) {
		return check("gh", err)
	}
	return fixableCheck("gh", err, "gh auth login")
}

func (d *Daemon) lingerCheck(ctx context.Context) protocol.HomeMoveCheck {
	account, err := user.Current()
	if err != nil {
		return check("linger", err)
	}
	cmd := exec.CommandContext(ctx, "loginctl", "show-user", account.Username, "--property=Linger", "--value")
	if env := d.loginEnv.Latest(ctx); len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.Output()
	if err != nil {
		return check("linger", fmt.Errorf("loginctl show-user %s: %v", account.Username, err))
	}
	if strings.TrimSpace(string(out)) != "yes" {
		fix := "loginctl enable-linger " + account.Username
		return fixableCheck("linger", fmt.Errorf("linger is off for %s, so the home would stop when the user logs out; run %s", account.Username, fix), fix)
	}
	return check("linger", nil)
}

func (d *Daemon) receiveHomeMoveStage(conn *bridge.Conn, incoming ssh.NewChannel) {
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	stream := conn.Bulk(d.life.Context(), channel, requests)
	defer stream.Close()
	var stage homeMoveStage
	reader := bufio.NewReader(stream)
	header, err := reader.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(header, &stage)
	}
	if err == nil {
		err = d.stageMovedHome(stage, reader)
	}
	reply := homeMoveStaged{}
	if err != nil {
		reply.Error = err.Error()
		d.logf("home move %s: staging failed: %v", stage.Record.ID, err)
		_ = homemove.Abort(d.dataRoot, stage.Record.ID)
	}
	_ = json.NewEncoder(stream).Encode(reply)
	_, _ = io.Copy(io.Discard, stream)
}

func (d *Daemon) stageMovedHome(stage homeMoveStage, stream io.Reader) error {
	record := stage.Record
	status := d.enrollmentStatus()
	switch {
	case record.Side != homemove.SideTo || record.To.ID != d.store.MachineID():
		return fmt.Errorf("move %s is not to this machine", record.ID)
	case !status.IsOutpost() || status.HomeID != record.HomeID:
		return fmt.Errorf("this machine is not an outpost of home %s", record.HomeID)
	case !strings.HasPrefix(record.ID, "m-") || strings.ContainsAny(record.ID, "/\\."):
		return fmt.Errorf("invalid move id %q", record.ID)
	}
	if err := homemove.CheckRoots(record.Roots); err != nil {
		return err
	}
	if err := d.forgetOtherStagedMoves(record.ID); err != nil {
		return err
	}
	dir := homemove.Dir(d.dataRoot, record.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	snapshot := homemove.Snapshot(d.dataRoot, record.ID)
	expected := map[string]homemove.File{}
	for _, file := range record.Files {
		expected["files/"+file.Path] = file
	}
	archive := tar.NewReader(stream)
	received := map[string]bool{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read the staged files: %w", err)
		}
		var target string
		want, isFile := expected[header.Name]
		switch {
		case header.Name == "attn.db":
			target = snapshot
		case isFile:
			target = filepath.Join(homemove.StagedFiles(d.dataRoot, record.ID), filepath.FromSlash(want.Path))
		default:
			return fmt.Errorf("the home sent %q, which a move does not carry", header.Name)
		}
		if isFile && want.Link != "" {
			if header.Typeflag != tar.TypeSymlink || header.Linkname != want.Link {
				return fmt.Errorf("%s arrived as something other than its link to %s", want.Path, want.Link)
			}
			if err := stageLink(target, want.Link); err != nil {
				return err
			}
			received[header.Name] = true
			continue
		}
		sum, size, err := writeStaged(target, archive)
		if err != nil {
			return err
		}
		if isFile && (sum != want.SHA256 || size != want.Size) {
			return fmt.Errorf("%s arrived as %d bytes with sha256 %s; the home sent %d bytes with sha256 %s", want.Path, size, sum, want.Size, want.SHA256)
		}
		if isFile && want.Mode != 0 {
			if err := os.Chmod(target, fs.FileMode(want.Mode)); err != nil {
				return err
			}
		}
		received[header.Name] = true
	}
	if !received["attn.db"] {
		return errors.New("the home's database did not arrive")
	}
	for name := range expected {
		if !received[name] {
			return fmt.Errorf("%s did not arrive", strings.TrimPrefix(name, "files/"))
		}
	}
	moved := stage.Home
	moved.TargetStream = d.analyticsStreamAtHome()
	if err := store.BuildMovedHome(d.life.Context(), moved, snapshot, config.DBPath(), homemove.StagedDatabase(d.dataRoot, record.ID)); err != nil {
		return fmt.Errorf("build the new home's database: %w", err)
	}
	encoded, err := json.Marshal(moved)
	if err != nil {
		return err
	}
	if err := os.WriteFile(movedHomePath(d.dataRoot, record.ID), encoded, 0o600); err != nil {
		return err
	}
	record.Step = homemove.StepStaged
	if err := homemove.Write(d.dataRoot, record); err != nil {
		return err
	}
	d.logf("home move %s: staged from %s", record.ID, record.From.Name)
	return nil
}

func stageLink(path, link string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(link, path)
}

func writeStaged(path string, from io.Reader) (string, int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), from)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (d *Daemon) forgetOtherStagedMoves(keep string) error {
	entries, err := os.ReadDir(filepath.Join(d.dataRoot, homemove.DirName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		record, err := homemove.Read(d.dataRoot, entry.Name())
		if err == nil && record.Step == homemove.StepStaged {
			if err := homemove.Abort(d.dataRoot, record.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Daemon) commitStagedHome(id string) error {
	record, err := homemove.Read(d.dataRoot, id)
	if err != nil {
		return fmt.Errorf("this machine has not staged move %s: %w", id, err)
	}
	if record.Side != homemove.SideTo {
		return fmt.Errorf("move %s is not to this machine", id)
	}
	switch record.Step {
	case homemove.StepCommitted:
	case homemove.StepStaged:
		crashpoint.At("home-move-target-committing")
		record.Step = homemove.StepCommitted
		if err := homemove.Write(d.dataRoot, record); err != nil {
			return err
		}
	default:
		return fmt.Errorf("move %s is %s here", id, record.Step)
	}
	if err := d.installHomeUnit(context.Background(), true); err != nil {
		return fmt.Errorf("install the systemd unit that starts the home: %w", err)
	}
	d.logf("home move %s: committed; restarting as the home", id)
	return nil
}

// homeMoveAdmit refuses a bridge from a home that claims this machine's own home id. When this machine took the home
// from that daemon by a move, the refusal says so, so the old home finishes its commit; when the old home rolled the
// move back, this machine rolls back too.
func (d *Daemon) homeMoveAdmit(home bridge.Identity) *bridge.Refusal {
	status := d.enrollmentStatus()
	if !status.IsHome() || home.HomeID == "" || home.HomeID != status.HomeID {
		return nil
	}
	name := d.bridgeIdentity().Name
	record, found, _ := homemove.Latest(d.dataRoot)
	switch {
	case found && record.Side == homemove.SideTo && record.Step == homemove.StepApplied && home.DaemonID == record.From.DaemonID:
		if home.Move != nil && home.Move.ID == record.ID && home.Move.Step == string(homemove.StepRolledBack) {
			if err := d.rollBackTakenHome(record); err != nil {
				return &bridge.Refusal{Reason: "home", Detail: err.Error()}
			}
			return &bridge.Refusal{Reason: "moved", Detail: fmt.Sprintf("%s is rolling back move %s", name, record.ID)}
		}
		return &bridge.Refusal{Reason: "moved", Detail: fmt.Sprintf("%s holds home %s since move %s", name, status.HomeID, record.ID)}
	case found && record.Side == homemove.SideFrom && record.Step == homemove.StepCommitting && home.DaemonID == record.To.DaemonID:
		if _, _, err := d.finishOwnCommit(record); err != nil {
			return &bridge.Refusal{Reason: "home", Detail: err.Error()}
		}
		d.life.Go("homeMoveCommitRestart", exitForMove)
		return &bridge.Refusal{Reason: "home", Detail: fmt.Sprintf("%s finished move %s and restarts as an outpost of %s", name, record.ID, record.To.Name)}
	case found && record.Side == homemove.SideFrom && record.Step == homemove.StepRolledBack && home.DaemonID == record.To.DaemonID:
		return &bridge.Refusal{Reason: homeMoveRolledBack, Detail: fmt.Sprintf("%s rolled back move %s", name, record.ID)}
	}
	return &bridge.Refusal{Reason: "home", Detail: fmt.Sprintf("%s holds home %s itself", name, status.HomeID)}
}

const homeMoveRolledBack = "rolled-back"

// followRolledBackMove rolls back this machine's side of a move once the machine it took the home from refuses its
// bridge because it rolled the move back.
func (d *Daemon) followRolledBackMove(outpostID string, err error) {
	var refusal *bridge.Refusal
	if !errors.As(err, &refusal) || refusal.Reason != homeMoveRolledBack {
		return
	}
	record, found, _ := homemove.Latest(d.dataRoot)
	if !found || record.Side != homemove.SideTo || record.Step != homemove.StepApplied || record.From.ID != outpostID || !strings.Contains(refusal.Detail, record.ID) {
		return
	}
	if err := d.rollBackTakenHome(record); err != nil {
		d.logf("home move %s: %v", record.ID, err)
	}
}

func (d *Daemon) rollBackTakenHome(record homemove.Record) error {
	record.Step = homemove.StepRollingBack
	if err := homemove.Write(d.dataRoot, record); err != nil {
		return err
	}
	d.logf("home move %s: %s rolled the move back; rolling back here", record.ID, record.From.Name)
	d.life.Go("homeMoveRollbackRestart", exitForMove)
	return nil
}

// keepNotebookAsCopy has the old home sync each Notebook folder where it already is. The folders are in sync with the
// new home, so they are prepared copies, and an outpost moves aside only a copy it has not prepared.
func keepNotebookAsCopy(dataRoot string) error {
	path := filepath.Join(dataRoot, filepath.FromSlash(homemove.NotebookSyncState))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var disk notebookSyncDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	for id, entry := range disk.Folders {
		marker := filepath.Join(entry.Root, ".attn", "notebook-sync")
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return err
		}
		entry.Initialized = true
		disk.Folders[id] = entry
	}
	out, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// followMovedNotebook points each crew member whose home was in a Notebook folder of the old home at that folder's
// copy here. A member in session keeps its home until a later start, when its session has ended.
func (d *Daemon) followMovedNotebook() {
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil || !found || record.Side != homemove.SideTo || record.Step != homemove.StepApplied || len(record.Notebook) == 0 {
		return
	}
	members, documents, err := d.readCrewMembers()
	if err != nil {
		d.logf("home move %s: read crew homes: %v", record.ID, err)
		return
	}
	for _, member := range members {
		home := member.HomeDir(d.dataRoot)
		for _, remap := range record.Notebook {
			moved := rebaseCrewPath(remap.From, remap.To, home)
			if moved == home {
				continue
			}
			if _, _, err := d.repointCrewHome(member, documents[member.ID].Rev, moved); err != nil {
				d.logf("home move %s: %s's home stays at %s: %v", record.ID, member.Name, home, err)
			}
			break
		}
	}
}

// finishMoveOnBridge records the old home's move done once it has bridged to the new home.
func (d *Daemon) finishMoveOnBridge(home string) {
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil || !found || record.Side != homemove.SideFrom || record.Step != homemove.StepApplied || record.To.DaemonID != home {
		return
	}
	record.Step = homemove.StepDone
	if err := homemove.Write(d.dataRoot, record); err != nil {
		d.logf("home move %s: record it done: %v", record.ID, err)
		return
	}
	if err := d.removeHomeUnit(d.life.Context()); err != nil {
		d.logf("home move %s: remove the systemd unit that started the home: %v", record.ID, err)
	}
	d.logf("home move %s: done; %s runs the home", record.ID, record.To.Name)
	pausepoint.At(pausepoint.HomeMoveFinished)
}

// homeReturned ends the wait of a home that rolled a move back once the move's target bridges as its outpost again.
func (d *Daemon) homeReturned(outpostID string) {
	d.homeMove.mu.Lock()
	yielded := d.homeMove.yielded
	d.homeMove.mu.Unlock()
	if !yielded {
		return
	}
	record, found, err := homemove.Latest(d.dataRoot)
	if err != nil || !found || record.Side != homemove.SideFrom || record.Step != homemove.StepRolledBack || record.To.ID != outpostID {
		return
	}
	record.TargetReturned = true
	if err := homemove.Write(d.dataRoot, record); err != nil {
		d.logf("home move %s: record that %s gave the home back: %v", record.ID, record.To.Name, err)
		return
	}
	d.logf("home move %s: %s is an outpost again; running the home", record.ID, record.To.Name)
	d.endHomeYield()
}

func (d *Daemon) endHomeYield() {
	d.homeMove.mu.Lock()
	d.homeMove.yielded = false
	d.homeMove.mu.Unlock()
	d.configurePhoneAccess()
	d.refreshJobQueueRole()
}

// analyticsStreamAtHome is the suffix this outpost's analytics streams carry at its home.
func (d *Daemon) analyticsStreamAtHome() string {
	secret, err := d.analyticsSecret()
	if err != nil || len(secret) == 0 {
		return ""
	}
	return string(analytics.Stream("").OnMachine(analytics.HashSubject(secret, d.enrollmentStatus().DaemonID)))
}
