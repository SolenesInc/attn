package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/builtin"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

type registeredClientCommand struct {
	blocksDuringRecovery bool
	run                  func(profile string, msg any) any
}

type adapterHost struct {
	d     *Daemon
	owner string
}

func (d *Daemon) host(owner string) harness.Host { return adapterHost{d: d, owner: owner} }

func (h adapterHost) Life() harness.Life                       { return &h.d.life }
func (h adapterHost) Logf(format string, args ...any)          { h.d.logf(format, args...) }
func (h adapterHost) Events() harness.Events                   { return h.d.events() }
func (h adapterHost) Setting(s *setting.Setting) string        { return h.d.setting(s) }
func (h adapterHost) LoginEnv() []string                       { return h.d.latestLoginEnv() }
func (h adapterHost) SpawnRoutingEnv() []string                { return h.d.spawnRoutingEnv() }
func (h adapterHost) Sessions() harness.HostSessions           { return hostSessions(h) }
func (h adapterHost) Files() harness.FileConversations         { return fileConversations{h.d} }
func (h adapterHost) Terminals() harness.HostTerminals         { return hostTerminals(h) }
func (h adapterHost) Records() harness.ServerRecords           { return hostRecords(h) }
func (h adapterHost) Clock() harness.Clock                     { return hostClock(h) }
func (h adapterHost) Notifications() harness.HostNotifications { return hostNotifications(h) }
func (h adapterHost) Profiles() harness.HostProfiles           { return hostProfiles(h) }
func (h adapterHost) HeadlessRefused(task string) bool         { return h.d.headlessTaskRefused(task) }
func (h adapterHost) AgentSockets() bool                       { return !h.d.noAgentSockets }
func (h adapterHost) DataDir() string                          { return h.d.dataRoot }
func (h adapterHost) DaemonSocket() string                     { return h.d.socketPath }

func (h adapterHost) Executable(facts harness.Facts, configured string) (string, error) {
	return h.d.machineExecutableOf(facts, configured)
}

func (h adapterHost) RunBinary(ctx context.Context, binary string, args ...string) ([]byte, error) {
	loginEnv := h.d.loginEnv.Latest(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = pty.MergeEnvironment(os.Environ(), loginEnv)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (h adapterHost) RegisterCron(kind string, interval time.Duration, runOnStart bool, fn func(context.Context) error) func() {
	h.d.registerHomeCron(homeCron{
		kind:       kind,
		interval:   interval,
		runOnStart: runOnStart,
		handler: func(ctx context.Context, _ *jobs.Job) (any, error) {
			return nil, fn(ctx)
		},
	})
	return func() {
		queue := h.d.jobQueueRef()
		if queue == nil {
			return
		}
		if _, err := queue.Enqueue(kind, jobs.EnqueueOptions{UniqueKey: jobs.CronKey, RunNow: true}); err != nil {
			h.d.logf("cron %s: enqueue: %v", kind, err)
		}
	}
}

func (h adapterHost) RegisterClientCommand(cmd string, blocksDuringRecovery bool, fn func(profile string, msg any) any) {
	if h.d.clientCommands == nil {
		h.d.clientCommands = make(map[string]registeredClientCommand)
	}
	h.d.clientCommands[cmd] = registeredClientCommand{blocksDuringRecovery: blocksDuringRecovery, run: fn}
}

func (h adapterHost) RegisterSocketCommand(cmd string, fn func(ctx context.Context, msg any) protocol.Response) {
	if h.d.socketCommands == nil {
		h.d.socketCommands = make(map[string]func(ctx context.Context, msg any) protocol.Response)
	}
	h.d.socketCommands[cmd] = fn
}

func (h adapterHost) RegisterCatalogCommand(name string, handler command.Handler) {
	if h.d.catalogCommands == nil {
		h.d.catalogCommands = make(map[string]command.Handler)
	}
	h.d.catalogCommands[name] = handler
}

func (h adapterHost) RegisterProfileSnapshot(s harness.ProfileSnapshot) func(profiles ...string) {
	h.d.profileSnapshots = append(h.d.profileSnapshots, s)
	return func(profiles ...string) {
		if len(profiles) == 0 {
			all, err := h.d.store.ListProfiles(false)
			if err != nil {
				h.d.logf("profile snapshot %s: %v", s.Snapshot, err)
				return
			}
			for _, profile := range all {
				profiles = append(profiles, profile.ID)
			}
		}
		h.d.coalesceSnapshots(func() {
			for _, profile := range profiles {
				h.d.publishFact(s.Fact, profile, nil)
			}
		})
	}
}

func (h adapterHost) RepositoryOrigin(repository string) string {
	return h.d.repositoryOrigin(gitTask{Kind: gitTaskSessionIdentity, Lane: gitInteractive}, repository)
}

type hostClock adapterHost

func (c hostClock) NewTimer(name stepclock.Name, d time.Duration) stepclock.Timer {
	return c.d.clock.NewTimer(name, d)
}

func (c hostClock) AfterFunc(name stepclock.Name, d time.Duration, f func()) stepclock.Timer {
	return c.d.clock.AfterFunc(name, d, f)
}

func (h adapterHost) ServerTools(subject string) (harness.AgentTools, bool) {
	tools := h.d.agentToolsFor(agentCredential{kind: credentialServer, name: subject})
	if tools == nil {
		return harness.AgentTools{}, false
	}
	catalog := agentToolsCatalog()
	return harness.AgentTools{URL: tools.URL, Credential: tools.Credential, Name: catalog.Name, Description: catalog.Description, Instructions: catalog.Instructions}, true
}

func (h adapterHost) TerminalTools(t harness.TerminalID) (harness.AgentTools, bool) {
	tools := h.d.agentTools(t)
	if tools == nil {
		return harness.AgentTools{}, false
	}
	catalog := agentToolsCatalog()
	return harness.AgentTools{URL: tools.URL, Credential: tools.Credential, Name: catalog.Name, Description: catalog.Description, Instructions: catalog.Instructions}, true
}

func (h adapterHost) ExecutableHealth(facts harness.Facts, offered bool) harness.Health {
	return h.d.executableHealth(facts, offered)
}

type hostSessions adapterHost

func (h hostSessions) BeginLaunch(id harness.SessionID, owner, run, route string) {
	h.d.sessionMonitor(id).BeginLaunch(owner, run, route)
}

func (h hostSessions) Get(id harness.SessionID) *protocol.Session { return h.d.store.Get(id) }
func (h hostSessions) List() []*protocol.Session                  { return h.d.store.List("") }
func (h hostSessions) Holding(conversation string) []harness.SessionID {
	return h.d.store.OpenSessionsHolding(conversation)
}
func (h hostSessions) Holder(profile, conversation string) harness.SessionID {
	return h.d.store.OpenSessionHolding(profile, conversation)
}
func (h hostSessions) StoredConversation(id harness.SessionID) (string, string) {
	c := h.d.store.GetSessionConversation(id)
	return c.NativeID, c.TranscriptPath
}
func (h hostSessions) ConversationOwner(session harness.SessionID, conversation string) harness.SessionID {
	return h.d.store.ConversationOwner(session, conversation)
}
func (h hostSessions) Resolve(ref, profile string) (harness.SessionID, error) {
	match, err := h.d.resolveTargetSession(ref, profile)
	if err != nil {
		return "", sessionRefusal(ref, err, command.Refuse(command.CodeSessionNotFound, "The target session was not found"))
	}
	return match.ID(), nil
}
func (h hostSessions) LaunchedAt(id harness.SessionID) time.Time {
	return h.d.store.SessionLaunchedAt(id)
}
func (h hostSessions) Lifetime(id harness.SessionID) (harness.ID, harness.Lifetime) {
	agent, lifetime := h.d.store.SessionLifetime(id)
	return harness.ID(agent), lifetime
}
func (h hostSessions) Conversation(id harness.SessionID) string { return h.d.serverConversation(id) }
func (h hostSessions) Hidden(id harness.SessionID) bool         { return h.d.hidden(id) }
func (h hostSessions) MirrorLabel(id harness.SessionID)         { h.d.mirrorLabel(id) }
func (h hostSessions) StartedDirectories(profile string) []string {
	return h.d.store.StartedConversationDirectories(profile)
}
func (h hostSessions) LaunchInfo(id harness.SessionID, agent harness.ID) harness.SessionLaunchInfo {
	route, _ := h.d.intendedApprovalRoute(id)
	member, bound := h.d.crewMemberForSession(id)
	return harness.SessionLaunchInfo{
		ContextCap:    h.d.launchContextWindowCap(id, string(agent)),
		ApprovalRoute: route,
		CrewBound:     bound,
		AwarenessDirs: member.AwarenessDirs,
	}
}
func (h hostSessions) Instructions(id harness.SessionID) string {
	launch, _, err := h.d.prepareLaunchContext(id, false)
	if err != nil {
		h.d.logf("no attn instructions for session %s: %v", id, err)
		return ""
	}
	return launch.Instructions()
}
func (h hostSessions) Reload(id harness.SessionID, cols, rows int) error {
	return h.d.reloadSession(id, cols, rows)
}
func (h hostSessions) LaunchUnconfirmed(id harness.SessionID, reason string) {
	h.d.resolveLaunchWatch(id, launchOutcome{unconfirmed: reason})
}
func (h hostSessions) EnsureTranscriptWatcher(id harness.SessionID, path string) {
	if monitor := h.d.sessionMonitor(id); monitor != nil {
		monitor.EnsureTranscriptWatcher(path)
	}
}
func (h hostSessions) FinishUsage(id harness.SessionID) {
	h.d.sessionMonitor(id).FinishUsage()()
}
func (h hostSessions) Remote(id harness.SessionID) bool { return h.d.sessionHost(id) != "" }
func (h hostSessions) PrimaryTerminal(id harness.SessionID) harness.TerminalID {
	return h.d.primaryTerminal(id)
}
func (h hostSessions) Showing(t harness.TerminalID) (harness.SessionID, bool) { return h.d.shownIn(t) }
func (h hostSessions) ChannelUp(id harness.SessionID) bool                    { return h.d.channelUp(id) }

func (h hostSessions) Evidence(id harness.SessionID) (harness.SessionEvidence, bool) {
	monitor := h.d.sessionMonitor(id)
	evidence, ok := monitor.Evidence()
	if !ok {
		return harness.SessionEvidence{}, false
	}
	status := evidence.ProgramStatus
	return harness.SessionEvidence{
		TurnOpen:              evidence.TurnOpen,
		ToolOpen:              evidence.ToolOpen,
		Compacting:            evidence.Compacting,
		TurnEnded:             turnHasEnded(monitor),
		ObservedCompletion:    monitor.ObservedServerCompletion(),
		TerminalClaimsWorking: status != nil && status.Source == sessionstate.SourceProgramStatus && status.Claim == sessionstate.ClaimBusy,
		FollowsServer:         status != nil && status.Source == sessionstate.SourceAppServer,
	}, true
}

func (h hostSessions) TraceClaim(id harness.SessionID, quiet bool, status string, at time.Time) {
	source := sessionmonitor.StateSourceServerReport
	if quiet {
		source = sessionmonitor.StateSourceServerQuiet
	}
	h.d.traceStateEvidence(id, sessionmonitor.Origin{Source: source, ObservedAt: at}, status)
}

func (h hostSessions) TraceServerStatus(id harness.SessionID, turn harness.Turn, at time.Time) {
	status := pty.ProgramIdle
	switch turn {
	case harness.TurnRunning:
		status = pty.ProgramWorking
	case harness.TurnApproval:
		status = pty.ProgramBlockedPermission
	case harness.TurnQuestion:
		status = pty.ProgramBlockedQuestion
	}
	h.d.traceStateEvidence(id, sessionmonitor.Origin{Source: string(sessionstate.SourceAppServer), ObservedAt: at}, status)
}

type hostTerminals adapterHost

func (h hostTerminals) Live(ctx context.Context) map[harness.TerminalID]struct{} {
	return h.d.liveTerminals(ctx)
}

func (h hostTerminals) LocalLive(ctx context.Context) map[harness.TerminalID]struct{} {
	return h.d.localLiveTerminals(ctx)
}

func (h hostTerminals) Start(ctx context.Context, spec harness.TerminalSpec) error {
	loginEnv, err := h.d.launchEnv(ctx)
	if err != nil {
		return err
	}
	agent := spec.Agent
	if agent == "" {
		agent = string(spec.ID)
	}
	env := []string{"ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_AGENT="}
	for _, key := range harness.CallerEnvKeys(builtin.CallerEnvs()...) {
		env = append(env, key+"=")
	}
	env = append(env, spec.Env...)
	return h.d.startAdapterTerminal(ctx, h.owner, ptybackend.SpawnOptions{
		ID: spec.ID, Agent: agent, Label: spec.Label, CWD: spec.Dir, Cols: 80, Rows: 24,
		Executable:    spec.Executable,
		AgentArgv:     spec.Argv,
		AgentEnv:      env,
		LoginShellEnv: loginEnv,
		DaemonEnv:     h.d.spawnRoutingEnv(),
	})
}

func (h hostTerminals) Adopt(ctx context.Context, t harness.TerminalID) {
	h.d.adoptAdapterTerminal(ctx, h.owner, t)
}

func (h hostTerminals) Owned(t harness.TerminalID) bool {
	owner, _ := h.d.adapterTerminalOwner(t)
	return owner == h.owner
}

func (h hostTerminals) Info(ctx context.Context, t harness.TerminalID) (harness.TerminalInfo, bool, error) {
	provider, ok := h.d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return harness.TerminalInfo{}, false, nil
	}
	info, err := provider.SessionInfo(ctx, t)
	if err != nil {
		if errors.Is(err, pty.ErrSessionNotFound) {
			return harness.TerminalInfo{}, true, os.ErrNotExist
		}
		return harness.TerminalInfo{}, true, err
	}
	out := harness.TerminalInfo{Running: info.Running, PID: info.PID, Cols: info.Cols, Rows: info.Rows}
	if paramsProvider, ok := h.d.ptyBackend.(ptybackend.SessionLaunchParamsProvider); ok {
		if params, err := paramsProvider.SessionLaunchParams(ctx, t); err == nil {
			out.Executable = params.Executable
		}
	}
	return out, true, nil
}

func (h hostTerminals) Kill(ctx context.Context, t harness.TerminalID, sig syscall.Signal) error {
	err := h.d.ptyBackend.Kill(ctx, t, sig)
	if errors.Is(err, pty.ErrSessionNotFound) {
		return os.ErrNotExist
	}
	return err
}

func (h hostTerminals) Remove(ctx context.Context, t harness.TerminalID) error {
	h.d.forgetAdapterTerminal(t)
	if err := h.d.removeTerminalRuntime(ctx, t); err != nil && !errors.Is(err, pty.ErrSessionNotFound) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (h hostTerminals) RemoveExited(ctx context.Context, t harness.TerminalID, exited harness.Exited) error {
	return h.d.removeExitedRuntime(ctx, t, exited.Lifecycle)
}

func (h adapterHost) RunInShellTile(ctx context.Context, profile, desktop, label, command string) (bool, error) {
	shell := pty.GetUserLoginShell()
	if !pty.ShellIntegrationExpected(shell, pty.MergeEnvironment(os.Environ(), h.d.latestLoginEnv())) {
		return false, harness.NoShellMarksError{Shell: shell}
	}
	provider, err := h.d.terminalText()
	if err != nil {
		return false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	id := protocol.SessionID(uuid.NewString())
	spawn := &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, ProfileID: profile, Agent: protocol.AgentShellValue, Cwd: home, Cols: 80, Rows: 24, Label: protocol.Ptr(label)}
	policy := internalSpawnPolicy{launchPlacement: &launchPlacement{desktopID: desktop, direction: layouttree.DirectionVertical}, userStarted: true}
	if rejection := h.d.runSpawnPipeline(spawn, policy); rejection != nil {
		return false, fmt.Errorf("open a terminal tile for %s: %w", label, rejection.reason())
	}
	terminal := h.d.primaryTerminal(id)

	firstCtx, cancel := context.WithTimeout(ctx, terminalFirstPromptTripwire)
	first, err := provider.WaitBlock(firstCtx, terminal, pty.WaitBlockParams{Until: "prompt", FirstPrompt: true})
	cancel()
	switch {
	case errors.Is(firstCtx.Err(), context.DeadlineExceeded):
		return true, fmt.Errorf("the shell in the new tile showed no prompt within limit=%s (%s); nothing was typed, so run %s there yourself", terminalFirstPromptTripwire, terminalFirstPromptReceipt, command)
	case err != nil:
		return true, fmt.Errorf("wait for the new tile's prompt: %w", err)
	case first.Exited:
		return true, errors.New("the new tile's shell exited before its first prompt")
	}
	state, err := provider.ReadText(ctx, terminal, "screen", 0)
	if err != nil {
		return true, err
	}
	last := state.LastBlockID
	if err := h.d.writeTerminalPTY(ctx, terminal, []byte(command+"\r"), "agent"); err != nil {
		return true, fmt.Errorf("type %s into the new tile: %w", label, err)
	}
	finished, err := provider.WaitBlock(ctx, terminal, pty.WaitBlockParams{Until: "prompt", AfterBlockID: &last})
	switch {
	case err != nil || finished.Exited:
		return true, fmt.Errorf("the tile closed before %s finished", label)
	case finished.Block != nil && finished.Block.ExitCode != nil && *finished.Block.ExitCode != 0:
		return true, harness.ShellExitError{Code: int(*finished.Block.ExitCode)}
	}
	return true, nil
}

type hostNotifications adapterHost

func (h hostNotifications) List() ([]harness.Notification, error) {
	if h.d.store == nil {
		return nil, nil
	}
	records, err := h.d.store.ListNotifications("")
	if err != nil {
		return nil, err
	}
	out := make([]harness.Notification, 0, len(records))
	for _, n := range records {
		out = append(out, harness.Notification{
			ID:         n.ID,
			Kind:       n.Kind,
			Severity:   string(n.Severity),
			Title:      n.Title,
			Body:       n.Body,
			Trigger:    n.Trigger,
			Impact:     n.Impact,
			SourceKind: n.SourceKind,
			SourceID:   n.SourceID,
		})
	}
	return out, nil
}

func (h hostNotifications) Ensure(n harness.Notification) error {
	if h.d.store == nil {
		return nil
	}
	actions := make([]store.NotificationAction, 0, len(n.Actions))
	for _, a := range n.Actions {
		actions = append(actions, store.NotificationAction{Kind: a.Kind, Label: a.Label})
	}
	record, created, err := h.d.store.EnsureNotification(store.NotificationRecord{
		ID:         n.ID,
		Kind:       n.Kind,
		Severity:   store.NotificationSeverity(n.Severity),
		Title:      n.Title,
		Body:       n.Body,
		Trigger:    n.Trigger,
		Impact:     n.Impact,
		Actions:    actions,
		SourceKind: n.SourceKind,
		SourceID:   n.SourceID,
	}, time.Now())
	if err != nil {
		return err
	}
	if created {
		h.d.publishFact(FactNotificationCreated, record.ID, nil)
	}
	return nil
}

func (h hostNotifications) Resolve(id string) error {
	if h.d.store == nil {
		return nil
	}
	resolved, err := h.d.store.ResolveNotification(id, time.Now())
	if err != nil {
		return err
	}
	if resolved {
		h.d.publishFact(FactNotificationRead, id, nil)
	}
	return nil
}

type hostProfiles adapterHost

func (h hostProfiles) List() ([]string, error) {
	all, err := h.d.store.ListProfiles(false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(all))
	for _, p := range all {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

func (h hostProfiles) Resolve(caller harness.SessionID, requested string) (string, string, error) {
	p, err := h.d.resolveGardenProfile(caller, requested, "")
	if err != nil {
		return "", "", err
	}
	return p.ID, p.Name, nil
}

func (d *Daemon) projectProfileSnapshot(snap harness.ProfileSnapshot) {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snap.Snapshot, func() {
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profile := client.selectedProfile()
			if profile == "" {
				return
			}
			data, ok := snapshots[profile]
			if !ok {
				var err error
				if data, err = json.Marshal(snap.Changed(profile)); err != nil {
					d.logf("profile snapshot %s: %v", snap.Snapshot, err)
					return
				}
				snapshots[profile] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}

type hostRecords adapterHost

func (h hostRecords) TerminalViews(kind string) ([]harness.TerminalView, error) {
	stored, err := h.d.store.TerminalViews(kind)
	views := make([]harness.TerminalView, 0, len(stored))
	for _, v := range stored {
		views = append(views, harness.TerminalView{Terminal: harness.TerminalID(v.TerminalID), Kind: v.Link, Profile: v.ProfileID})
	}
	return views, err
}

func (h hostRecords) SaveTerminalView(v harness.TerminalView) error {
	return h.d.store.SaveTerminalView(store.TerminalView{TerminalID: string(v.Terminal), Link: v.Kind, ProfileID: v.Profile})
}

func (h hostRecords) DeleteTerminalView(t harness.TerminalID) error {
	return h.d.store.DeleteTerminalView(string(t))
}

func (h hostRecords) Feed(id harness.SessionID) harness.FeedRecord {
	feed := h.d.store.ServerFeed(id)
	return harness.FeedRecord{Entry: feed.Entry, UsageBaseline: feed.UsageBaseline, Completion: h.d.store.SessionRuntimeRecords(id).ServerCompletion}
}

func (h hostRecords) SaveFeedEntry(id harness.SessionID, entry int64) {
	h.d.store.SaveServerFeedEntry(id, entry)
}
func (h hostRecords) SaveUsageBaseline(id harness.SessionID, baseline string) {
	h.d.store.SaveServerUsageBaseline(id, baseline)
}
func (h hostRecords) SubagentFeed(id harness.SessionID, conversation string) harness.FeedRecord {
	feed := h.d.store.SubagentServerFeed(id, conversation)
	return harness.FeedRecord{Entry: feed.Entry, UsageBaseline: feed.UsageBaseline}
}
func (h hostRecords) SaveSubagentFeedEntry(id harness.SessionID, conversation string, entry int64) {
	h.d.store.SaveSubagentServerFeedEntry(id, conversation, entry)
}
func (h hostRecords) SaveSubagentUsageBaseline(id harness.SessionID, conversation, baseline string) {
	h.d.store.SaveSubagentServerUsageBaseline(id, conversation, baseline)
}
func (h hostRecords) ConversationSeen(conversation string) bool {
	return h.d.store.ServerConversationSeen(conversation)
}
