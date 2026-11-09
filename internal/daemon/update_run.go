package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/selfupdate"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	notificationKindUpdateRequest = "update_requested"
	notificationActionUpdate      = "start_update"
	notificationActionDecline     = "decline_update"
	updateNoticeSource            = "self-update"
)

type updateRuns struct {
	mu sync.Mutex
}

func updateRunToProtocol(run *store.UpdateRun) *protocol.UpdateRun {
	if run == nil {
		return nil
	}
	wire := &protocol.UpdateRun{
		ID:          run.ID,
		TargetBuild: run.TargetBuild,
		InstallKind: run.InstallKind,
		Phase:       protocol.UpdatePhase(run.Phase),
		LogPath:     run.LogPath,
		Requesters:  run.Requesters,
	}
	if wire.Requesters == nil {
		wire.Requesters = []string{}
	}
	if run.Outcome != "" {
		wire.Outcome = protocol.Ptr(run.Outcome)
	}
	return wire
}

func (d *Daemon) latestUpdateRun() *store.UpdateRun {
	run, err := d.store.LatestUpdateRun()
	if err != nil {
		d.logf("update run: %v", err)
		return nil
	}
	return run
}

func (d *Daemon) latestUpdateRunForApp() *protocol.UpdateRun {
	if !d.on(setting.SelfUpdateEnabled) {
		return nil
	}
	return updateRunToProtocol(d.latestUpdateRun())
}

func (d *Daemon) projectUpdateRun() {
	run := d.latestUpdateRunForApp()
	if run == nil {
		return
	}
	d.wsHub.BroadcastValue(protocol.UpdateChangedMessage{Event: protocol.EventUpdateChanged, Run: *run})
}

func (d *Daemon) updatePlan() (selfupdate.Plan, error) {
	return selfupdate.NewPlan(d.install(), d.dataRoot, config.InstanceOriginWorktree(d.dataRoot))
}

func (d *Daemon) handleUpdateRequest(conn net.Conn, msg *protocol.UpdateRequestMessage) {
	if !d.on(setting.SelfUpdateEnabled) {
		d.sendError(conn, setting.SelfUpdateEnabled.Refusal().Error())
		return
	}
	if _, err := d.updatePlan(); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	caller := protocol.SessionID(protocol.Deref(msg.CallerSessionID))
	profile, err := d.resolveGardenProfile(caller, protocol.Deref(msg.ProfileID), "")
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	result, err := d.requestUpdate(profile.ID, string(caller), protocol.Deref(msg.Reason))
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, UpdateRequestResult: result})
}

func (d *Daemon) requestUpdate(profileID, requester, reason string) (*protocol.UpdateRequestResult, error) {
	d.updateRuns.mu.Lock()
	defer d.updateRuns.mu.Unlock()
	now := time.Now()
	request := store.UpdateRequest{ID: uuid.NewString(), ProfileID: profileID, Requester: requester, Reason: reason}
	if run := d.latestUpdateRun(); run != nil && run.Open() {
		if err := d.store.AddUpdateRequest(request, store.UpdateRequestStarted, run.ID, now); err != nil {
			return nil, err
		}
		d.publishFact(FactUpdateChanged, run.ID, nil)
		joined := d.latestUpdateRun()
		return &protocol.UpdateRequestResult{RequestID: request.ID, JoinedRun: updateRunToProtocol(joined)}, nil
	}
	pending, err := d.store.PendingUpdateRequests()
	if err != nil {
		return nil, err
	}
	pending = updateRequestsOfProfile(pending, profileID)
	if err := d.resolveRequestNotifications(pending); err != nil {
		return nil, err
	}
	note, _, err := d.store.EnsureNotification(store.NotificationRecord{
		ID:       notificationKindUpdateRequest + ":" + request.ID,
		Kind:     notificationKindUpdateRequest,
		Severity: store.NotificationInfo,
		Title:    "Update attn?",
		Body:     "An agent asked to update attn.",
		Trigger:  updateRequestTrigger(append(pending, request)),
		Impact:   "Update installs the new attn and restarts it. Your agents keep running.",
		Actions: []store.NotificationAction{
			{Kind: notificationActionUpdate, Label: "Update", TargetID: protocol.SessionID(request.ID)},
			{Kind: notificationActionDecline, Label: "Decline", TargetID: protocol.SessionID(request.ID)},
		},
		SourceKind: "update",
		SourceID:   request.ID,
		ProfileID:  profileID,
	}, now)
	if err != nil {
		return nil, err
	}
	request.NotificationID = note.ID
	if err := d.store.AddUpdateRequest(request, store.UpdateRequestPending, "", now); err != nil {
		return nil, err
	}
	d.publishFact(FactNotificationCreated, note.ID, nil)
	return &protocol.UpdateRequestResult{RequestID: request.ID}, nil
}

func updateRequestsOfProfile(requests []store.UpdateRequest, profileID string) []store.UpdateRequest {
	var inProfile []store.UpdateRequest
	for _, request := range requests {
		if request.ProfileID == profileID {
			inProfile = append(inProfile, request)
		}
	}
	return inProfile
}

func updateRequestTrigger(requests []store.UpdateRequest) string {
	lines := make([]string, len(requests))
	for i, request := range requests {
		lines[i] = "An agent asked to update attn."
		if request.Reason != "" {
			lines[i] = "An agent asked to update attn: " + request.Reason
		}
	}
	return strings.Join(lines, "\n")
}

func (d *Daemon) handleStartUpdate(client *wsClient, msg *protocol.StartUpdateMessage) {
	result := protocol.StartUpdateResultMessage{Event: protocol.EventStartUpdateResult, RequestID: msg.RequestID, Success: true}
	run, err := d.startUpdate()
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Run = updateRunToProtocol(run)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) startUpdate() (*store.UpdateRun, error) {
	if !d.on(setting.SelfUpdateEnabled) {
		return nil, setting.SelfUpdateEnabled.Refusal()
	}
	d.updateRuns.mu.Lock()
	defer d.updateRuns.mu.Unlock()
	if run := d.latestUpdateRun(); run != nil && run.Open() {
		if err := d.attachPendingRequests(run.ID); err != nil {
			return nil, err
		}
		return d.latestUpdateRun(), nil
	}
	plan, err := d.updatePlan()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(d.dataRoot, "update-logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("create the update log folder %s: %w", logDir, err)
	}
	id := uuid.NewString()
	run, err := d.store.CreateUpdateRun(id, string(plan.Kind), string(protocol.UpdatePhasePreparing), filepath.Join(logDir, id+".log"), time.Now())
	if err != nil {
		return nil, err
	}
	if err := d.attachPendingRequests(run.ID); err != nil {
		return nil, err
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
	d.life.Go("runUpdate", func() { d.runUpdate(run, plan) })
	return d.latestUpdateRun(), nil
}

func (d *Daemon) attachPendingRequests(runID string) error {
	pending, err := d.store.PendingUpdateRequests()
	if err != nil {
		return err
	}
	if err := d.resolveRequestNotifications(pending); err != nil {
		return err
	}
	return d.store.AttachPendingUpdateRequests(runID)
}

func (d *Daemon) resolveRequestNotifications(requests []store.UpdateRequest) error {
	seen := map[string]bool{}
	for _, request := range requests {
		if request.NotificationID == "" || seen[request.NotificationID] {
			continue
		}
		seen[request.NotificationID] = true
		resolved, err := d.store.ResolveNotification(request.NotificationID, time.Now())
		if err != nil {
			return err
		}
		if resolved {
			d.publishFact(FactNotificationRead, request.NotificationID, nil)
		}
	}
	return nil
}

func (d *Daemon) handleDeclineUpdate(client *wsClient, msg *protocol.DeclineUpdateMessage) {
	result := protocol.DeclineUpdateResultMessage{Event: protocol.EventDeclineUpdateResult, RequestID: msg.RequestID, Success: true}
	if err := d.declineUpdate(msg.UpdateRequestID); err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}

func (d *Daemon) declineUpdate(requestID string) error {
	d.updateRuns.mu.Lock()
	defer d.updateRuns.mu.Unlock()
	pending, err := d.store.PendingUpdateRequests()
	if err != nil {
		return err
	}
	index := slices.IndexFunc(pending, func(request store.UpdateRequest) bool { return request.ID == requestID })
	if index < 0 {
		return nil
	}
	profileID := pending[index].ProfileID
	declined := updateRequestsOfProfile(pending, profileID)
	if err := d.resolveRequestNotifications(declined); err != nil {
		return err
	}
	if err := d.store.DeclinePendingUpdateRequests(profileID); err != nil {
		return err
	}
	for _, request := range declined {
		d.tellRequester(request.Requester, "update-declined/"+request.ID, "The user declined your request to update attn.")
	}
	return nil
}

func (d *Daemon) tellRequester(requester, id, text string) {
	if requester == "" {
		return
	}
	if _, err := d.sendToInbox(inbox.Item{ID: id, To: who.ToSession(protocol.SessionID(requester)), Kind: inbox.Notice, Source: updateNoticeSource, Hint: "attn update", Text: text}); err != nil {
		d.logf("update run: tell %s: %v", requester, err)
	}
}

func (d *Daemon) setUpdatePhase(run store.UpdateRun, phase protocol.UpdatePhase) {
	if err := d.store.SetUpdateRunPhase(run.ID, string(phase)); err != nil {
		d.logf("update run %s: set phase %s: %v", run.ID, phase, err)
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
}

func (d *Daemon) finishUpdateRun(run store.UpdateRun, phase protocol.UpdatePhase, outcome string) {
	d.updateRuns.mu.Lock()
	err := d.store.FinishUpdateRun(run.ID, string(phase), outcome, time.Now())
	requesters := run.Requesters
	if latest := d.latestUpdateRun(); latest != nil && latest.ID == run.ID {
		requesters = latest.Requesters
	}
	d.updateRuns.mu.Unlock()
	if err != nil {
		d.logf("update run %s: finish: %v", run.ID, err)
	}
	d.publishFact(FactUpdateChanged, run.ID, nil)
	for _, requester := range requesters {
		d.tellRequester(requester, "update-run/"+run.ID+"/"+requester, "attn update finished: "+outcome)
	}
}

func (d *Daemon) failUpdateRun(run store.UpdateRun, cause string) {
	d.logf("update run %s failed: %s", run.ID, cause)
	d.finishUpdateRun(run, protocol.UpdatePhaseFailed, fmt.Sprintf("%s. Log: %s", strings.TrimSuffix(cause, "."), run.LogPath))
}

func (d *Daemon) updateEnvironment(plan selfupdate.Plan) []string {
	env := config.EnvOutsideSessions(pty.MergeEnvironment(os.Environ(), d.latestLoginEnv()))
	return append(env, "REPOSITORY_PATH="+plan.Repository)
}

func (d *Daemon) runUpdate(run store.UpdateRun, plan selfupdate.Plan) {
	log, err := os.OpenFile(run.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		d.failUpdateRun(run, fmt.Sprintf("could not open the build log: %v", err))
		return
	}
	defer log.Close()
	ctx := d.life.Context()
	env := d.updateEnvironment(plan)
	if finished := d.getUpdateBuild(ctx, run, plan, env, log); finished {
		return
	}
	staged, err := selfupdate.ReadStagedBuild(ctx, plan.StagedDaemon())
	if err != nil {
		d.failUpdateRun(run, err.Error())
		return
	}
	target := bridge.Build{Version: staged.Version, SourceFingerprint: staged.SourceFingerprint, ProtocolVersion: staged.ProtocolVersion}
	run.TargetBuild = target.String()
	if running := d.bridgeIdentity().Build.String(); run.TargetBuild == running {
		d.finishUpdateRun(run, protocol.UpdatePhaseDone, "Already running "+running+".")
		return
	}
	if err := d.store.SetUpdateRunTarget(run.ID, run.TargetBuild); err != nil {
		d.failUpdateRun(run, err.Error())
		return
	}
	if err := selfupdate.CheckDatabase(ctx, plan.StagedDaemon(), config.DBPath(), target); err != nil {
		d.failUpdateRun(run, err.Error())
		return
	}
	d.setUpdatePhase(run, protocol.UpdatePhaseStaging)
	d.setUpdatePhase(run, protocol.UpdatePhaseApplying)
	if err := d.startUpdateHelper(run, plan, env, log); err != nil {
		d.failUpdateRun(run, err.Error())
	}
}

// getUpdateBuild puts the build in the plan's folder. It reports true when the
// run has already finished, failed or with nothing to do.
func (d *Daemon) getUpdateBuild(ctx context.Context, run store.UpdateRun, plan selfupdate.Plan, env []string, log *os.File) bool {
	limit := selfupdate.BuildTimeoutFromEnv()
	limited, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	fail := func(what string, err error) bool {
		if errors.Is(limited.Err(), context.DeadlineExceeded) {
			d.failUpdateRun(run, fmt.Sprintf("the %s did not finish within its %s limit (%s)", what, limit, selfupdate.BuildTimeoutEnv))
			return true
		}
		d.failUpdateRun(run, err.Error())
		return true
	}
	if plan.Kind == selfupdate.ReleaseApp || plan.Kind == selfupdate.ReleaseBinary {
		latest, newer, err := plan.Download(limited, buildinfo.ReleaseSource, buildinfo.Version, env, log)
		if err != nil {
			return fail("download", err)
		}
		if !newer {
			d.finishUpdateRun(run, protocol.UpdatePhaseDone, "Already on the latest release, "+latest.Version+".")
			return true
		}
		return false
	}
	commit, err := plan.Checkout(ctx, env, log)
	if err != nil {
		d.failUpdateRun(run, err.Error())
		return true
	}
	if commit != "" && !buildinfo.Dirty && buildinfo.GitCommit != "" && strings.HasPrefix(commit, buildinfo.GitCommit) {
		d.finishUpdateRun(run, protocol.UpdatePhaseDone, "Already on the latest commit, "+commit[:9]+".")
		return true
	}
	if err := plan.Build(limited, env, log); err != nil {
		return fail("build", fmt.Errorf("the build failed: %w", err))
	}
	return false
}

func (d *Daemon) startUpdateHelper(run store.UpdateRun, plan selfupdate.Plan, env []string, log *os.File) error {
	args := []string{"_update-apply", "--run", run.ID, "--kind", string(plan.Kind), "--dir", plan.Dir, "--root", plan.Root, "--instance", plan.Instance}
	helper := exec.Command(plan.StagedDaemon(), args...)
	helper.Dir = d.dataRoot
	helper.Env = env
	helper.Stdout = log
	helper.Stderr = log
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := helper.Start(); err != nil {
		return fmt.Errorf("start the update helper: %w", err)
	}
	d.life.Go("awaitUpdateHelper", func() {
		err := helper.Wait()
		cause := selfupdate.TakeFailure(d.dataRoot, run.ID)
		if cause == "" {
			cause = fmt.Sprintf("the update helper exited without restarting attn: %v", err)
		}
		d.failUpdateRun(run, cause)
	})
	return nil
}

// reconcileUpdateRun runs at every start. The run is the helper's only way to
// hand back to a daemon: the build this daemon is decides how the run ended.
func (d *Daemon) reconcileUpdateRun() {
	_ = os.Remove(selfupdate.RestartMarkerPath(d.dataRoot))
	run := d.latestUpdateRun()
	if run == nil || !run.Open() {
		return
	}
	running := d.bridgeIdentity().Build.String()
	if run.TargetBuild == "" || run.TargetBuild != running {
		cause := selfupdate.TakeFailure(d.dataRoot, run.ID)
		if cause == "" {
			cause = "the update was interrupted: attn restarted before it finished"
		}
		d.failUpdateRun(*run, cause)
		return
	}
	d.setUpdatePhase(*run, protocol.UpdatePhaseHomeRestarted)
	d.setUpdatePhase(*run, protocol.UpdatePhaseOutposts)
	d.finishUpdateRun(*run, protocol.UpdatePhaseDone, "attn is now running "+running+".")
}
