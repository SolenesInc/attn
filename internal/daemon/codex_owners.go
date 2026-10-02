package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *codexRuntime) ownerContext(owner *store.CodexOwner) (codexLaunchContext, error) {
	var launch codexLaunchContext
	if err := json.Unmarshal(owner.Context, &launch); err != nil {
		return launch, fmt.Errorf("read owner %s launch context: %w", owner.SessionID, err)
	}
	return launch, nil
}

func (r *codexRuntime) rollbackLaunch(id string) {
	r.mu.Lock()
	if _, exists := r.views[id]; exists {
		if err := r.removeViewLocked(id); err != nil {
			r.d.logf("rollback shared Codex launch %s: %v", id, err)
		}
	}
	r.mu.Unlock()
	r.cleanupReservation(id)
}

func (r *codexRuntime) prepareLaunch(opts *ptybackend.SpawnOptions, session *protocol.Session, initialName string) error {
	if _, err := r.d.ensureWorkspaceSessionPane(session.WorkspaceID, session.ID, session.Label); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, err := r.d.store.CodexOwner(session.ID)
	if err != nil {
		return err
	}
	view := store.CodexView{RuntimeID: opts.ID, ServerID: r.serverID, LaunchOwnerID: session.ID, Generation: codexViewGenerationPrefix + uuid.NewString(), Resolution: "unresolved"}
	if owner == nil {
		launch := codexLaunchContext{InitialName: initialName, CWD: opts.CWD, WorkspaceID: session.WorkspaceID, Executable: opts.Executable, Model: opts.Model, Effort: opts.Effort, Yolo: opts.YoloMode, AutoApprove: opts.AutoApprove, TrustWorkingDirectory: opts.TrustWorkingDirectory, Guidance: hooks.Launch{Garden: r.d.requireHome(garden.Surface) == nil, InjectWorkflow: opts.WorkflowGuidanceEnabled, Crew: r.d.crewPrimeForLaunch(session.ID)}}
		if unattended := opts.UnattendedLaunch; !unattended.IsZero() {
			launch.Model, launch.Effort, launch.Executable = unattended.Model, unattended.Effort, unattended.Executable
			launch.AutoApprove = unattended.ApprovalDriverMode == "auto_review"
			launch.TrustWorkingDirectory = true
		}
		if r.d.isChiefOfStaffSession(session.ID) {
			launch.Guidance.NotebookRoot = r.d.store.GetSetting(SettingNotebookRootEffective)
		}
		raw, err := json.Marshal(launch)
		if err != nil {
			return err
		}
		owner = &store.CodexOwner{SessionID: session.ID, ServerID: r.serverID, Context: raw}
		if err := r.d.store.ReserveCodexOwnerAndView(*owner, view); err != nil {
			return err
		}
	} else if err := r.d.store.SaveCodexView(view); err != nil {
		return err
	}
	r.setViewLocked(view)
	crashAt(crashAfterCodexReservation)
	launch, err := r.ownerContext(owner)
	if err != nil {
		return err
	}
	if err := r.ensureServer(r.d.life.Context(), launch); err != nil {
		return err
	}
	if owner.Archived {
		if _, err := r.control.Call(r.d.life.Context(), "thread/unarchive", map[string]any{"threadId": owner.NativeRootID}); err != nil {
			return err
		}
		if err := r.d.store.SetCodexArchived(owner.SessionID, false); err != nil {
			return err
		}
	}
	if err := r.addViewLocked(view); err != nil {
		return err
	}
	opts.LifecycleID = view.Generation
	opts.ExternalEnv = append(opts.ExternalEnv, "ATTN_CODEX_REMOTE=unix://"+r.socket(opts.ID))
	return nil
}

func (r *codexRuntime) reserveNativeOwner(v store.CodexView, params map[string]any) (*store.CodexOwner, error) {
	sourceID := v.LaunchOwnerID
	if v.Resolution == "resolved" {
		sourceID = v.SessionID
	}
	source, err := r.d.store.CodexOwner(sourceID)
	if err != nil || source == nil {
		return nil, fmt.Errorf("missing launch owner %s: %v", sourceID, err)
	}
	if source.NativeRootID == "" && !r.initialConsumed[v.RuntimeID] {
		r.initialConsumed[v.RuntimeID] = true
		return source, nil
	}
	launch, err := r.ownerContext(source)
	if err != nil {
		return nil, err
	}
	if cwd, ok := params["cwd"].(string); ok && cwd != "" {
		launch.CWD = cwd
	}
	launch.InitialName = ""
	launch.Guidance.NotebookRoot = ""
	launch.Guidance.Crew = ""
	id := uuid.NewString()
	now := string(protocol.TimestampNow())
	session := &protocol.Session{ID: id, Agent: protocol.SessionAgentCodex, Directory: launch.CWD, WorkspaceID: launch.WorkspaceID, Label: filepath.Base(launch.CWD), State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now}
	if err := r.d.store.AddCheckedUnlessTeardown(session); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(launch)
	if err != nil {
		return nil, err
	}
	owner := &store.CodexOwner{SessionID: id, ServerID: r.serverID, Context: raw}
	if err := r.d.store.ReserveCodexOwner(*owner); err != nil {
		r.d.store.Remove(id)
		return nil, err
	}
	intent, _ := r.d.store.LaunchIntent(source.SessionID)
	intent.CodexMode = "shared"
	intent.ChiefOfStaff = false
	r.d.store.SetLaunchIntent(id, intent)
	if err := r.d.store.InitializeSessionCostTracking(id); err != nil {
		r.cleanupReservation(id)
		return nil, err
	}
	r.d.associateSessionWithWorkspace(id, launch.WorkspaceID)
	r.d.startEvidence(id, r.d.sharedCodexEvidence(launch))
	r.d.publishFact(FactSessionReregistered, id, nil)
	return owner, nil
}

func (r *codexRuntime) prepareRPC(runtimeID string, m *codexshared.Message) (func(*codexshared.Message), error) {
	if m.Method == "thread/name/set" {
		var p struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, err
		}
		owner, err := r.d.store.CodexOwnerByRoot(r.serverID, p.ThreadID)
		if err != nil || owner == nil {
			return nil, err
		}
		if err := r.d.store.ReplacePendingCodexName(owner.SessionID, p.ThreadID, p.Name); err != nil {
			return nil, err
		}
		revision := r.nameRevision(p.ThreadID)
		return func(reply *codexshared.Message) {
			if len(reply.Error) > 0 {
				return
			}
			if err := r.d.store.ConsumeCodexInitialName(owner.SessionID, p.ThreadID); err != nil {
				r.d.logf("Codex native rename for %s: %v", owner.SessionID, err)
				return
			}
			r.projectNativeName(p.ThreadID, &p.Name, revision, false)
		}, nil
	}
	if m.Method == "turn/start" {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, err
		}
		owner, err := r.d.store.CodexOwnerByRoot(r.serverID, p.ThreadID)
		if err == nil && owner != nil {
			err = r.applyInitialName(r.d.life.Context(), owner.SessionID)
		}
		return nil, err
	}
	if m.Method != "thread/start" && m.Method != "thread/resume" && m.Method != "thread/fork" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal(m.Params, &params); err != nil {
		return nil, err
	}
	if ephemeral, _ := params["ephemeral"].(bool); ephemeral {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views[runtimeID]
	if !ok {
		return nil, errors.New("codex view is closed")
	}
	var owner *store.CodexOwner
	var err error
	if m.Method == "thread/resume" {
		root, _ := params["threadId"].(string)
		owner, err = r.d.store.CodexOwnerByRoot(r.serverID, root)
		if err == nil && owner == nil {
			owner, err = r.adoptRoot(root, v)
		}
	} else {
		owner, err = r.reserveNativeOwner(v, params)
	}
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, errors.New("codex lifecycle request has no owner")
	}
	creation := m.Method != "thread/resume"
	prepared := false
	defer func() {
		if creation && !prepared {
			r.rejectCreationLocked(runtimeID, owner.SessionID)
		}
	}()
	launch, err := r.ownerContext(owner)
	if err != nil {
		return nil, err
	}
	if m.Method != "thread/resume" {
		if launch.Model == "" {
			launch.Model, _ = params["model"].(string)
		}
		launch.ApprovalPolicy, _ = json.Marshal(params["approvalPolicy"])
		launch.Sandbox, _ = params["sandbox"].(string)
		launch.Reviewer, _ = params["approvalsReviewer"].(string)
		if config, ok := params["config"].(map[string]any); ok && launch.Effort == "" {
			launch.Effort, _ = config["model_reasoning_effort"].(string)
		}
		raw, err := json.Marshal(launch)
		if err != nil {
			return nil, err
		}
		if err := r.d.store.UpdateCodexContext(owner.SessionID, raw); err != nil {
			return nil, err
		}
	}
	injectCodexOwner(params, owner.SessionID, r.d.socketPath, r.d.wrapperExecutable(), launch)
	m.Params, err = json.Marshal(params)
	if err != nil {
		return nil, err
	}
	method := m.Method
	nameRevision := r.nameRevision(owner.NativeRootID)
	r.activeMu.Lock()
	revision := r.activeTurns[owner.NativeRootID].Revision
	r.activeMu.Unlock()
	prepared = true
	return func(reply *codexshared.Message) {
		if len(reply.Error) > 0 {
			r.d.logf("Codex %s for owner %s in view %s: %s", method, owner.SessionID, runtimeID, reply.Error)
			if creation {
				r.mu.Lock()
				r.rejectCreationLocked(runtimeID, owner.SessionID)
				r.mu.Unlock()
			}
			return
		}
		var result struct {
			Thread codexNativeThread `json:"thread"`
		}
		if json.Unmarshal(reply.Result, &result) != nil || result.Thread.ID == "" {
			return
		}
		if !creation {
			r.mu.Lock()
			if err := r.reopenOwnerLocked(owner.SessionID); err != nil {
				r.d.logf("Codex native reopen %s: %v", owner.SessionID, err)
			}
			r.mu.Unlock()
		}
		r.bindOwner(owner.SessionID, result.Thread, revision)
		r.projectNativeName(result.Thread.ID, result.Thread.Name, nameRevision, false)
		if creation {
			err := r.applyInitialName(r.d.life.Context(), owner.SessionID)
			if err != nil {
				r.notifyInitialNameFailure(owner.SessionID, err)
				return
			}
			r.nameMu.Lock()
			name := r.names[result.Thread.ID].Name
			r.nameMu.Unlock()
			if name != nil {
				var raw map[string]json.RawMessage
				var thread map[string]json.RawMessage
				if json.Unmarshal(reply.Result, &raw) == nil && json.Unmarshal(raw["thread"], &thread) == nil {
					thread["name"], _ = json.Marshal(name)
					raw["thread"], _ = json.Marshal(thread)
					reply.Result, _ = json.Marshal(raw)
				}
			}
		}
	}, nil
}

type codexNativeThread struct {
	Name      *string           `json:"name"`
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	CWD       string            `json:"cwd"`
	Source    json.RawMessage   `json:"source"`
	Ephemeral bool              `json:"ephemeral"`
	Status    codexNativeStatus `json:"status"`
	Turns     []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turns"`
}

func (r *codexRuntime) adoptRoot(root string, v store.CodexView) (*store.CodexOwner, error) {
	r.activeMu.Lock()
	revision := r.activeTurns[root].Revision
	r.activeMu.Unlock()
	nameRevision := r.nameRevision(root)
	result, err := r.control.Call(r.d.life.Context(), "thread/read", map[string]any{"threadId": root, "includeTurns": false})
	if err != nil {
		return nil, err
	}
	var read struct {
		Thread codexNativeThread `json:"thread"`
	}
	if err := json.Unmarshal(result, &read); err != nil {
		return nil, err
	}
	if read.Thread.Ephemeral || strings.Contains(string(read.Thread.Source), "subAgent") {
		return nil, errors.New("native helper thread is not an independent Codex owner")
	}
	owner, err := r.reserveNativeOwner(v, map[string]any{"cwd": read.Thread.CWD})
	if err != nil {
		return nil, err
	}
	if err := r.d.store.BindCodexRoot(owner.SessionID, root); err != nil {
		return nil, err
	}
	owner.NativeRootID = root
	r.d.observeOrQueueAgentConversation(agentConversationObservation{SessionID: owner.SessionID, NativeID: root, TranscriptPath: read.Thread.Path})
	r.projectNativeSnapshot(read.Thread, revision)
	r.projectNativeName(root, read.Thread.Name, nameRevision, false)
	return owner, nil
}

func injectCodexOwner(params map[string]any, id, socket, wrapper string, launch codexLaunchContext) {
	params["cwd"] = launch.CWD
	config, _ := params["config"].(map[string]any)
	if config == nil {
		config = make(map[string]any)
		params["config"] = config
	}
	hooks.MergeCodexOwnerConfig(config, id, socket, wrapper, launch.Guidance)
	if guidance, ok := config["developer_instructions"].(string); ok {
		prior, _ := params["developerInstructions"].(string)
		params["developerInstructions"] = strings.TrimSpace(prior + "\n\n" + guidance)
		delete(config, "developer_instructions")
	}
	if launch.Model != "" {
		params["model"] = launch.Model
	}
	if launch.Effort != "" {
		config["model_reasoning_effort"] = launch.Effort
	}
	if len(launch.ApprovalPolicy) > 0 && string(launch.ApprovalPolicy) != "null" {
		var policy any
		if json.Unmarshal(launch.ApprovalPolicy, &policy) == nil {
			params["approvalPolicy"] = policy
		}
	}
	if launch.Sandbox != "" {
		params["sandbox"] = launch.Sandbox
	}
	if launch.Reviewer != "" {
		params["approvalsReviewer"] = launch.Reviewer
	}
	if launch.Yolo {
		params["approvalPolicy"] = "never"
		params["sandbox"] = "danger-full-access"
	} else if launch.AutoApprove {
		params["approvalPolicy"] = "on-request"
		config["approvals_reviewer"] = "auto_review"
	}
}

func (d *Daemon) wrapperExecutable() string {
	path := strings.TrimSpace(os.Getenv("ATTN_WRAPPER_PATH"))
	if path != "" {
		return path
	}
	path, _ = os.Executable()
	return path
}

func (r *codexRuntime) bindOwner(id string, t codexNativeThread, revision uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.d.store.BindCodexRoot(id, t.ID); err != nil {
		r.d.logf("Codex bind owner %s to %s: %v", id, t.ID, err)
		return
	}
	if t.CWD != "" {
		owner, err := r.d.store.CodexOwner(id)
		if err == nil && owner != nil {
			launch, err := r.ownerContext(owner)
			if err == nil {
				launch.CWD = t.CWD
				raw, err := json.Marshal(launch)
				if err == nil {
					err = r.d.store.UpdateCodexContext(id, raw)
				}
				if err != nil {
					r.d.logf("persist Codex cwd %s: %v", id, err)
				}
			}
		}
		session := r.d.store.Get(id)
		if session != nil && session.Directory != t.CWD {
			session.Directory = t.CWD
			if err := r.d.store.AddCheckedUnlessTeardown(session); err != nil {
				r.d.logf("Codex cwd %s: %v", id, err)
			}
		}
	}
	r.d.observeOrQueueAgentConversation(agentConversationObservation{SessionID: id, NativeID: t.ID, TranscriptPath: t.Path})
	r.projectNativeSnapshot(t, revision)
	if session := r.d.store.Get(id); session != nil && session.State == protocol.SessionStateLaunching {
		r.d.applyState(sessionStateChange{sessionID: id, state: string(protocol.SessionStateIdle), cause: liveSignal{}, origin: stateOrigin{source: "codex", detail: "native root bound"}})
	}
	for runtimeID, v := range r.views {
		if v.RawTitle != "" && v.Resolution != "disconnected" {
			r.resolveTitleLocked(runtimeID, v.RawTitle)
		}
	}
	// A control subscription keeps hidden roots alive independently of views.
	r.d.life.Go("codexHoldRoot", func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		params := map[string]any{"threadId": t.ID}
		owner, err := r.d.store.CodexOwner(id)
		if err != nil || owner == nil || owner.Archived {
			return
		}
		launch, err := r.ownerContext(owner)
		if err != nil {
			return
		}
		if err := r.ensureServer(r.d.life.Context(), launch); err != nil {
			r.d.logf("Codex hold root %s: %v", id, err)
			return
		}
		injectCodexOwner(params, id, r.d.socketPath, r.d.wrapperExecutable(), launch)
		if _, err := r.resumeOwner(r.d.life.Context(), r.control, params); err != nil {
			r.d.logf("Codex hold root %s: %v", id, err)
		}
	})
}

func (r *codexRuntime) rejectCreationLocked(runtimeID, ownerID string) {
	if view, live := r.views[runtimeID]; live && view.LaunchOwnerID == ownerID {
		delete(r.initialConsumed, runtimeID)
	} else {
		r.cleanupReservation(ownerID)
	}
}

func (r *codexRuntime) cleanupReservation(id string) {
	owner, err := r.d.store.CodexOwner(id)
	if err != nil || owner == nil || owner.NativeRootID != "" {
		return
	}
	if err := r.d.store.RemoveUnusedCodexOwner(id); err != nil {
		r.d.logf("Codex clean reservation %s: %v", id, err)
		return
	}
	session := r.d.store.Get(id)
	r.d.forgetSessionRuntime(id)
	r.d.store.Remove(id)
	r.d.forgetSessionTrace(id)
	r.d.clearChiefOfStaffIfSession(id)
	r.d.releaseCrewBindingIfSession(id)
	r.d.dissociateSessionFromWorkspace(id)
	r.d.publishSessionUnregistered(session)
}

type codexTurnState struct {
	ID       string
	Revision uint64
	Status   codexNativeStatus
}

func (r *codexRuntime) resumeOwner(ctx context.Context, control *codexshared.Client, params map[string]any) (json.RawMessage, error) {
	root, _ := params["threadId"].(string)
	r.activeMu.Lock()
	state := r.activeTurns[root]
	r.activeTurns[root] = state
	revision := state.Revision
	r.activeMu.Unlock()
	nameRevision := r.nameRevision(root)
	result, err := control.Call(ctx, "thread/resume", params)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Thread codexNativeThread `json:"thread"`
	}
	if err := json.Unmarshal(result, &reply); err != nil {
		return nil, err
	}
	r.projectNativeSnapshot(reply.Thread, revision)
	r.projectNativeName(reply.Thread.ID, reply.Thread.Name, nameRevision, false)
	return result, nil
}

func (r *codexRuntime) projectNativeSnapshot(thread codexNativeThread, revision uint64) {
	var activeID string
	for _, turn := range thread.Turns {
		if turn.Status == "inProgress" {
			activeID = turn.ID
		}
	}
	r.activeMu.Lock()
	state := r.activeTurns[thread.ID]
	// A native notification received during the request is newer than its snapshot.
	if state.Revision == revision {
		state.ID = activeID
		state.Status = thread.Status
		state.Revision++
		r.activeTurns[thread.ID] = state
	}
	r.activeMu.Unlock()
	r.projectNativeStatus(thread.ID)
}

type codexNativeStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

func (r *codexRuntime) controlDisconnected() {
	r.d.logf("shared Codex control disconnected; native attention needs reconciliation")
	owners, err := r.d.store.CodexOwners(r.serverID)
	if err != nil {
		r.d.logf("read shared Codex owners after control disconnect: %v", err)
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	for root, state := range r.activeTurns {
		state.ID = ""
		state.Status = codexNativeStatus{}
		state.Revision++
		r.activeTurns[root] = state
	}
	for _, owner := range owners {
		if owner.Archived || owner.NativeRootID == "" {
			continue
		}
		r.d.recordEvidence(owner.SessionID, time.Now(), func(e *sessionstate.Evidence) {
			e.NativeRoot = &sessionstate.Observation{Source: sessionstate.SourceNative, Claim: sessionstate.ClaimStopFailed, Detail: "control_disconnected", ObservedAt: time.Now()}
		})
	}
}

func (r *codexRuntime) observeControl(m codexshared.Message) {
	r.observeNative(m)
	r.observeNativeName(m)
	if m.Method != "thread/status/changed" {
		return
	}
	var params struct {
		ThreadID string            `json:"threadId"`
		Status   codexNativeStatus `json:"status"`
	}
	if json.Unmarshal(m.Params, &params) != nil || params.ThreadID == "" {
		return
	}
	r.activeMu.Lock()
	state := r.activeTurns[params.ThreadID]
	state.Status = params.Status
	state.Revision++
	r.activeTurns[params.ThreadID] = state
	r.activeMu.Unlock()
	r.projectNativeStatus(params.ThreadID)
}

func (r *codexRuntime) projectNativeStatus(root string) {
	owner, err := r.d.store.CodexOwnerByRoot(r.serverID, root)
	if err != nil || owner == nil || owner.Archived {
		return
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	status := r.activeTurns[root].Status
	var claim sessionstate.Claim
	switch status.Type {
	case "active":
		claim = sessionstate.ClaimBusy
		for _, flag := range status.ActiveFlags {
			if flag == "waitingOnApproval" {
				claim = sessionstate.ClaimApprovalPending
				break
			}
			if flag == "waitingOnUserInput" {
				claim = sessionstate.ClaimNeedsInput
			}
		}
	case "idle":
		claim = sessionstate.ClaimIdle
	case "systemError":
		claim = sessionstate.ClaimStopFailed
	case "notLoaded":
		r.d.updateEvidence(owner.SessionID, nil, func(e *sessionstate.Evidence) { e.NativeRoot = nil })
		return
	default:
		return
	}
	at := time.Now()
	r.d.traceStateEvidence(owner.SessionID, stateOrigin{source: "codex_native", detail: status.Type, observedAt: at}, string(claim))
	r.d.recordEvidence(owner.SessionID, at, func(e *sessionstate.Evidence) {
		e.NativeRoot = &sessionstate.Observation{Source: sessionstate.SourceNative, Claim: claim, Detail: status.Type, ObservedAt: at}
		if claim == sessionstate.ClaimBusy {
			e.LastBusyAt = at
		}
	})
}

func (r *codexRuntime) observeNative(m codexshared.Message) {
	if m.Method == "" {
		return
	}
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(m.Params, &params) != nil {
		return
	}
	if m.Method == "turn/started" || m.Method == "turn/completed" {
		r.activeMu.Lock()
		state, known := r.activeTurns[params.ThreadID]
		if m.Method == "turn/completed" && !known {
			r.activeMu.Unlock()
			return
		}
		if m.Method == "turn/started" {
			state.ID = params.Turn.ID
		} else if state.ID == params.Turn.ID {
			state.ID = ""
		}
		state.Revision++
		r.activeTurns[params.ThreadID] = state
		r.activeMu.Unlock()
	}
}

func (r *codexRuntime) send(ctx context.Context, id, text string, active bool) error {
	if err := r.applyInitialName(ctx, id); err != nil {
		return err
	}
	control, root, err := func() (*codexshared.Client, string, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		owner, err := r.d.store.CodexOwner(id)
		if err != nil {
			return nil, "", err
		}
		if owner == nil || owner.NativeRootID == "" {
			return nil, "", fmt.Errorf("codex owner %s has no native root", id)
		}
		launch, err := r.ownerContext(owner)
		if err != nil {
			return nil, "", err
		}
		if err := r.ensureServer(ctx, launch); err != nil {
			return nil, "", err
		}
		return r.control, owner.NativeRootID, nil
	}()
	if err != nil {
		return err
	}
	r.activeMu.Lock()
	turnID := r.activeTurns[root].ID
	r.activeMu.Unlock()
	params := map[string]any{"threadId": root, "input": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}}
	method := "turn/start"
	if active && turnID != "" {
		method = "turn/steer"
		params["expectedTurnId"] = turnID
	}
	_, err = control.Call(ctx, method, params)
	return err
}

func (r *codexRuntime) reconfigureOwner(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, err := r.d.store.CodexOwner(id)
	if err != nil {
		return err
	}
	if owner == nil || owner.NativeRootID == "" {
		return fmt.Errorf("codex owner %s has not initialized", id)
	}
	launch, err := r.ownerContext(owner)
	if err != nil {
		return err
	}
	launch.Guidance.Crew = r.d.crewPrimeForLaunch(id)
	launch.Guidance.NotebookRoot = ""
	if r.d.isChiefOfStaffSession(id) {
		launch.Guidance.NotebookRoot = r.d.store.GetSetting(SettingNotebookRootEffective)
	}
	raw, err := json.Marshal(launch)
	if err != nil {
		return err
	}
	if err := r.d.store.UpdateCodexContext(id, raw); err != nil {
		return err
	}
	if err := r.ensureServer(r.d.life.Context(), launch); err != nil {
		return err
	}
	params := map[string]any{"threadId": owner.NativeRootID}
	injectCodexOwner(params, id, r.d.socketPath, r.d.wrapperExecutable(), launch)
	_, err = r.resumeOwner(r.d.life.Context(), r.control, params)
	return err
}
