package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/builtin"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

const pluginDriverCallTimeout = 30 * time.Second

type pluginDriverRegisterParams struct {
	Agent        string                 `json:"agent"`
	DisplayName  string                 `json:"display_name,omitempty"`
	Capabilities map[string]bool        `json:"capabilities,omitempty"`
	Server       *appserver.Declaration `json:"server,omitempty"`
	SkillDirs    []pluginSkillDir       `json:"skill_dirs,omitempty"`
	Logo         *pluginLogo            `json:"logo,omitempty"`
}

type pluginLogo struct {
	View float64 `json:"view"`
	Body string  `json:"body"`
}

type pluginDriverRegisterResult struct {
	OK         bool              `json:"ok"`
	ActiveRuns []activePluginRun `json:"active_runs,omitempty"`
}

type activePluginRun struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	RunID      string              `json:"run_id"`
	Metadata   json.RawMessage     `json:"metadata,omitempty"`
	Seq        uint64              `json:"seq"`
}

func (d *Daemon) activePluginRuns(pluginName string) []activePluginRun {
	active := d.store.ListAgentDriverRuns(pluginName)
	runs := make([]activePluginRun, 0, len(active))
	for _, run := range active {
		item := activePluginRun{TerminalID: d.primaryTerminal(run.SessionID), RunID: run.RunID, Seq: run.Seq}
		if json.Valid([]byte(run.Metadata)) {
			item.Metadata = json.RawMessage(run.Metadata)
		}
		runs = append(runs, item)
	}
	return runs
}

type pluginReportStateParams struct {
	TerminalID    protocol.TerminalID `json:"session_id"`
	SessionID     protocol.SessionID  `json:"-"`
	RunID         string              `json:"run_id"`
	Seq           uint64              `json:"seq"`
	State         string              `json:"state"`
	OnlyIfUnknown bool                `json:"only_if_unknown,omitempty"`
}

type pluginReportStopParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	SessionID  protocol.SessionID  `json:"-"`
	RunID      string              `json:"run_id"`
	Seq        uint64              `json:"seq"`
	Verdict    string              `json:"verdict"`
}

type pluginReportMetadataParams struct {
	TerminalID      protocol.TerminalID `json:"session_id"`
	SessionID       protocol.SessionID  `json:"-"`
	RunID           string              `json:"run_id"`
	Seq             uint64              `json:"seq"`
	Metadata        json.RawMessage     `json:"metadata"`
	ResumeSessionID string              `json:"resume_session_id,omitempty"`
}

type pluginReportPullRequestParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	SessionID  protocol.SessionID  `json:"-"`
	RunID      string              `json:"run_id"`
	URL        string              `json:"url"`
}

type pluginReportTranscriptPathParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	SessionID  protocol.SessionID  `json:"-"`
	RunID      string              `json:"run_id"`
	Path       string              `json:"path"`
}

type pluginReportTitleParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	SessionID  protocol.SessionID  `json:"-"`
	RunID      string              `json:"run_id"`
	Title      string              `json:"title"`
}

type pluginReportPromptParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	SessionID  protocol.SessionID  `json:"-"`
	RunID      string              `json:"run_id"`
	Text       string              `json:"text"`
}

type pluginClassifyStopParams struct {
	TerminalID    protocol.TerminalID `json:"session_id"`
	SessionID     protocol.SessionID  `json:"-"`
	RunID         string              `json:"run_id"`
	AssistantText string              `json:"assistant_text"`
}

type pluginClassifyStopResult struct {
	Verdict string `json:"verdict"`
}

type pluginReportCompactionParams struct {
	TerminalID     protocol.TerminalID `json:"session_id"`
	RunID          string              `json:"run_id"`
	InputID        string              `json:"input_id"`
	ConversationID string              `json:"conversation_id"`
	Outcome        string              `json:"outcome"`
	Reason         string              `json:"reason,omitempty"`
}

type pendingPluginReport struct {
	State      *pluginReportStateParams
	Stop       *pluginReportStopParams
	Metadata   *pluginReportMetadataParams
	Transcript *pluginReportTranscriptPathParams
	Title      *pluginReportTitleParams
	Prompt     *pluginReportPromptParams
}

func (r *pluginRegistry) registerDriver(plugin *pluginConnection, params pluginDriverRegisterParams, bindServer func() error) error {
	agent := normalizePluginAgent(params.Agent)
	if agent == "" {
		return errors.New("driver.register params.agent must contain lowercase letters, numbers, hyphens, or underscores")
	}
	if agent == protocol.AgentShellValue || builtin.Has(agent) {
		return fmt.Errorf("agent %q is reserved by attn", agent)
	}
	capabilities, err := validatePluginDriverCapabilities(params.Capabilities)
	if err != nil {
		return err
	}
	var logo harness.Logo
	if params.Logo != nil {
		if logo, err = harness.NewLogo(params.Logo.View, params.Logo.Body); err != nil {
			return fmt.Errorf("driver.register params.logo: %w", err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins[plugin.name] != plugin {
		return fmt.Errorf("plugin %q is not connected", plugin.name)
	}
	if existing, exists := r.drivers[agent]; exists && existing.Plugin != plugin.name {
		return fmt.Errorf("agent %q is already registered by plugin %q", agent, existing.Plugin)
	}
	if err := bindServer(); err != nil {
		return err
	}
	skillDirs := params.SkillDirs
	r.drivers[agent] = harness.PluginDriver{
		Plugin:       plugin.name,
		Agent:        agent,
		Capabilities: capabilities,
		DisplayName:  strings.TrimSpace(params.DisplayName),
		Logo:         logo,
		SkillDirs: func(lookupEnv func(string) string, homeDir string) ([]string, error) {
			return resolvePluginSkillsDirs(skillDirs, lookupEnv, homeDir), nil
		},
	}
	close(r.driverRegistered)
	r.driverRegistered = make(chan struct{})
	return nil
}

func (r *pluginRegistry) driverConnection(agent, owner string) (harness.PluginDriver, *pluginConnection) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.drivers[agent]
	if !ok || reg.Plugin != owner {
		return harness.PluginDriver{}, nil
	}
	return reg, r.plugins[owner]
}

func (d *Daemon) resolvePluginDriverRoutes(plugin, agent string) {
	route, ok := d.runRoute(harness.SessionFacts{Harness: agent, RunOwner: plugin})
	if !ok {
		return
	}
	for _, run := range d.store.ListAgentDriverRuns(plugin) {
		session := d.store.Get(run.SessionID)
		if session != nil && session.Agent == agent {
			d.sessionMonitor(run.SessionID).ResolveRoute(plugin, run.RunID, route)
			d.kickSessionInboxAddresses(run.SessionID)
			d.kickCompactingClose(run.SessionID)
		}
	}
}

func (r *pluginRegistry) driverOrRegistration(agent string) (registered bool, nextRegistration <-chan struct{}) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, registered = r.drivers[strings.TrimSpace(strings.ToLower(agent))]
	return registered, r.driverRegistered
}

func (r *pluginRegistry) driver(agent string) (harness.PluginDriver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	driver, ok := r.drivers[strings.TrimSpace(strings.ToLower(agent))]
	return driver, ok
}

func (r *pluginRegistry) registeredDrivers() []harness.PluginDriver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	drivers := make([]harness.PluginDriver, 0, len(r.drivers))
	for _, driver := range r.drivers {
		drivers = append(drivers, driver)
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Agent < drivers[j].Agent })
	return drivers
}

func normalizePluginAgent(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_'))
		if !valid {
			return ""
		}
	}
	return value
}

func validatePluginDriverCapabilities(values map[string]bool) (map[string]bool, error) {
	allowed := map[string]struct{}{
		"server_binding":         {},
		"resume":                 {},
		"resume_availability":    {},
		"yolo":                   {},
		"initial_prompt":         {},
		"classifier":             {},
		"state_reporting":        {},
		"pending_approval":       {},
		"message_delivery":       {},
		"attributed_input":       {},
		"safe_during_question":   {},
		"compaction":             {},
		"model_pin":              {},
		"model_discovery":        {},
		"effort_pin":             {},
		"launch_instructions":    {},
		"pull_request_reporting": {},
		"agent_tools":            {},
	}
	out := make(map[string]bool, len(values))
	for name, enabled := range values {
		name = strings.TrimSpace(strings.ToLower(name))
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unsupported driver capability %q", name)
		}
		out[name] = enabled
	}
	for _, name := range []string{"attributed_input", "safe_during_question", "compaction"} {
		if out[name] && !out["message_delivery"] {
			return nil, fmt.Errorf("driver capability %q requires message_delivery", name)
		}
	}
	return out, nil
}

func (d *Daemon) handlePluginDriverMethod(plugin *pluginConnection, msg jsonRPCMessage) (interface{}, bool, error) {
	switch msg.Method {
	case "driver.register":
		var params pluginDriverRegisterParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode driver.register params: %w", err)
		}
		if err := d.registerPluginDriver(plugin, params); err != nil {
			return nil, true, err
		}
		d.logf("plugin driver registered plugin=%s agent=%s", plugin.name, normalizePluginAgent(params.Agent))
		d.resolvePluginDriverRoutes(plugin.name, normalizePluginAgent(params.Agent))
		if l := d.appServer.Link(normalizePluginAgent(params.Agent)); l != nil && l.Plugin() == plugin.name {
			l.Recover(d.life.Context(), harness.RecoverProcesses)
			l.Recover(d.life.Context(), harness.RecoverViews)
		}
		d.pushMonitorPolicies()
		d.publishFact(FactPluginDriverRegistered, plugin.name, nil)
		d.life.Go("reviveAgentsOfRegisteredDriver", func() {
			if !d.isRecovering() {
				d.reviveRecoverableAgents("")
			}
		})
		result := pluginDriverRegisterResult{OK: true, ActiveRuns: d.activePluginRuns(plugin.name)}
		return result, true, nil
	case "session.report_compaction":
		var params pluginReportCompactionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, err
		}
		session := d.sessionInTerminal(params.TerminalID)
		if err := d.authorizePluginSessionReport(plugin, session, params.RunID); err != nil {
			return nil, true, err
		}
		target := pluginCompactTarget{Plugin: plugin.name, Run: params.RunID, Conversation: params.ConversationID}
		if d.store.GetSessionConversation(session).NativeID != params.ConversationID || !d.compactions.pluginMatches(session, params.InputID, target) {
			return nil, true, errors.New("compaction report does not match the pending run, input and conversation")
		}
		switch params.Outcome {
		case "busy":
			d.events().Compaction(session, harness.Compaction{Input: params.InputID, Result: harness.CompactionBusy, Reason: params.Reason})
		case "completed":
			d.events().Compaction(session, harness.Compaction{Input: params.InputID, Result: harness.Compacted})
		case "failed":
			reason := params.Reason
			if reason == "" {
				reason = "plugin compaction failed"
			}
			d.events().Compaction(session, harness.Compaction{Input: params.InputID, Result: harness.CompactionFailed, Reason: reason})
		default:
			return nil, true, fmt.Errorf("unsupported compaction outcome %q", params.Outcome)
		}
		return struct{}{}, true, nil
	case "session.report_state":
		var params pluginReportStateParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_state params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		if err := validatePluginReportedState(params); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{State: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedState(params)
		}
		return struct{}{}, true, nil
	case "session.report_stop":
		var params pluginReportStopParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_stop params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		if err := validatePluginReportedStop(params); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Stop: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedStop(params)
		}
		return struct{}{}, true, nil
	case "session.report_metadata":
		var params pluginReportMetadataParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_metadata params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		if len(params.Metadata) == 0 || !json.Valid(params.Metadata) {
			return nil, true, errors.New("session.report_metadata metadata must be valid JSON")
		}
		if err := validatePluginReportCursor(params.RunID, params.Seq); err != nil {
			return nil, true, err
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Metadata: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			if wait := d.applyPluginReportedMetadata(params); wait != nil {
				return wait, true, nil
			}
		}
		return struct{}{}, true, nil
	case "session.report_pull_request":
		var params pluginReportPullRequestParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_pull_request params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
			return nil, true, err
		}
		d.notePluginDriverReport(params.SessionID)
		if err := d.events().Tool(params.SessionID, harness.ToolUse{PullRequests: []string{params.URL}}); err != nil {
			return nil, true, err
		}
		return struct{}{}, true, nil
	case "session.report_transcript_path":
		var params pluginReportTranscriptPathParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_transcript_path params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		params.Path = strings.TrimSpace(params.Path)
		if params.Path == "" {
			return nil, true, errors.New("session.report_transcript_path path is required")
		}
		if !filepath.IsAbs(params.Path) {
			return nil, true, fmt.Errorf("session.report_transcript_path path %q must be absolute", params.Path)
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Transcript: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedTranscriptPath(params)
		}
		return struct{}{}, true, nil
	case "session.report_title":
		var params pluginReportTitleParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_title params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		params.Title = strings.Join(strings.Fields(params.Title), " ")
		if params.Title == "" {
			return nil, true, errors.New("session.report_title title is required")
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Title: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedTitle(params)
		}
		return struct{}{}, true, nil
	case "session.report_prompt":
		var params pluginReportPromptParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return nil, true, fmt.Errorf("decode session.report_prompt params: %w", err)
		}
		params.SessionID = d.sessionInTerminal(params.TerminalID)
		params.Text = strings.TrimSpace(params.Text)
		if params.Text == "" {
			return nil, true, errors.New("session.report_prompt text is required")
		}
		if !d.queueReportDuringPluginLaunch(plugin, params.SessionID, pendingPluginReport{Prompt: &params}) {
			if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
				return nil, true, err
			}
			d.applyPluginReportedPrompt(params)
		}
		return struct{}{}, true, nil
	default:
		return nil, false, nil
	}
}

func (d *Daemon) authorizePluginSessionReport(plugin *pluginConnection, sessionID protocol.SessionID, runID string) error {
	if d.ensurePluginRegistry().get(plugin.name) != plugin {
		return errors.New("plugin connection is no longer active")
	}
	sessionID = protocol.TrimID(sessionID)
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("unknown session %q", sessionID)
	}
	cursor := d.store.GetAgentDriverRun(sessionID)
	if cursor.PluginName != plugin.name || cursor.RunID != strings.TrimSpace(runID) {
		return fmt.Errorf("plugin %q does not own active run %q for session %q", plugin.name, strings.TrimSpace(runID), sessionID)
	}
	return nil
}

func (d *Daemon) handlePluginClassifyStop(plugin *pluginConnection, msg jsonRPCMessage) {
	var params pluginClassifyStopParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, fmt.Sprintf("decode attn.classify_stop params: %v", err)))
		return
	}
	text := strings.TrimSpace(params.AssistantText)
	if text == "" {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, "assistant_text is required"))
		return
	}
	params.SessionID = d.sessionInTerminal(params.TerminalID)
	if err := d.authorizePluginSessionReport(plugin, params.SessionID, params.RunID); err != nil {
		_ = plugin.send(jsonRPCFailure(msg.ID, jsonRPCInvalidRequest, err.Error()))
		return
	}
	session := d.store.Get(params.SessionID)
	d.sessionMonitor(params.SessionID).RecordRunAnswer(text)
	id := msg.ID
	d.life.Go("classifyPluginStop", func() {
		verdict, err := d.classifyPluginStop(session, text)
		if err != nil {
			verdict = protocol.StateUnknown
		}
		_ = plugin.send(jsonRPCResult(id, pluginClassifyStopResult{Verdict: verdict}))
	})
}

var pluginReportedTurns = map[string]harness.Turn{
	protocol.StateUnknown:         harness.TurnUnknown,
	protocol.StateWorking:         harness.TurnRunning,
	protocol.StatePendingApproval: harness.TurnApproval,
	protocol.StateWaitingInput:    harness.TurnQuestion,
	protocol.StateIdle:            harness.TurnEnded,
}

func validatePluginReportedState(params pluginReportStateParams) error {
	if _, ok := pluginReportedTurns[strings.TrimSpace(params.State)]; !ok {
		return fmt.Errorf("unsupported session state %q", params.State)
	}
	return validatePluginReportCursor(params.RunID, params.Seq)
}

func validatePluginReportCursor(runID string, seq uint64) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("run_id is required")
	}
	if seq == 0 {
		return errors.New("seq must be greater than zero")
	}
	return nil
}

func (d *Daemon) applyPluginReportedState(params pluginReportStateParams) {
	d.notePluginDriverReport(params.SessionID)
	d.events().Turn(harness.FromSession(params.SessionID), time.Now(), harness.TurnEvent{
		Turn:     pluginReportedTurns[strings.TrimSpace(params.State)],
		Epoch:    params.RunID,
		Seq:      params.Seq,
		Restated: params.OnlyIfUnknown,
	})
}

func validatePluginReportedStop(params pluginReportStopParams) error {
	verdict := strings.TrimSpace(params.Verdict)
	switch verdict {
	case protocol.StateIdle, protocol.StateWaitingInput, protocol.StateUnknown:
	default:
		return fmt.Errorf("unsupported stop verdict %q", params.Verdict)
	}
	return validatePluginReportCursor(params.RunID, params.Seq)
}

func (d *Daemon) applyPluginReportedStop(params pluginReportStopParams) {
	d.applyPluginReportedState(pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       params.Seq,
		State:     strings.TrimSpace(params.Verdict),
	})
}

func (d *Daemon) applyPluginReportedMetadata(params pluginReportMetadataParams) conversationWait {
	d.notePluginDriverReport(params.SessionID)
	if !d.sessionMonitor(params.SessionID).ReportRunNote(params.RunID, params.Seq, string(params.Metadata)) {
		d.logf("plugin metadata report discarded: session=%s run=%s seq=%d", params.SessionID, params.RunID, params.Seq)
		return nil
	}
	if resumeID := strings.TrimSpace(params.ResumeSessionID); resumeID != "" {
		return d.events().Moved(harness.FromSession(params.SessionID), harness.Move{Conversation: resumeID})
	}
	return nil
}

func (d *Daemon) applyPluginReportedTranscriptPath(params pluginReportTranscriptPathParams) {
	d.notePluginDriverReport(params.SessionID)
	if !d.sessionMonitor(params.SessionID).ReportRunPath(params.RunID, params.Path) {
		d.logf("plugin transcript path report discarded: session=%s run=%s", params.SessionID, params.RunID)
	}
}

func (d *Daemon) applyPluginReportedTitle(params pluginReportTitleParams) {
	d.notePluginDriverReport(params.SessionID)
	d.events().Named(params.SessionID, params.Title)
}

func (d *Daemon) applyPluginReportedPrompt(params pluginReportPromptParams) {
	d.notePluginDriverReport(params.SessionID)
	d.events().Prompt(harness.FromSession(params.SessionID), time.Now(), harness.Prompt{
		Text:   params.Text,
		Live:   true,
		Titles: true,
	})
}

func (d *Daemon) queueReportDuringPluginLaunch(plugin *pluginConnection, sessionID protocol.SessionID, report pendingPluginReport) bool {
	var watch *launchWatch
	queued := d.sessionMonitor(sessionID).QueueDuringLaunch(plugin.name, report.runID(), func() { d.replayPluginReport(report) }, func() {
		if report.State != nil || report.Stop != nil {
			watch = d.claimLaunchWatch(sessionID)
		}
	})
	if !queued {
		return false
	}
	watch.settle(launchOutcome{startedAt: time.Now()})
	return true
}

func (d *Daemon) replayPluginReport(report pendingPluginReport) {
	switch {
	case report.State != nil:
		d.applyPluginReportedState(*report.State)
	case report.Stop != nil:
		d.applyPluginReportedStop(*report.Stop)
	case report.Metadata != nil:
		var err error
		if wait := d.applyPluginReportedMetadata(*report.Metadata); wait != nil {
			err = wait(d.life.Context())
		}
		if err != nil {
			d.logf("plugin metadata replay: session=%s: %v", report.Metadata.SessionID, err)
		}
	case report.Transcript != nil:
		d.applyPluginReportedTranscriptPath(*report.Transcript)
	case report.Title != nil:
		d.applyPluginReportedTitle(*report.Title)
	case report.Prompt != nil:
		d.applyPluginReportedPrompt(*report.Prompt)
	}
}

func (d *Daemon) queueExitDuringPluginLaunch(sessionID protocol.SessionID, info ptybackend.ExitInfo) bool {
	var watch *launchWatch
	queued := d.sessionMonitor(sessionID).QueueExitDuringLaunch(info, func() {
		d.logf("deferring plugin PTY exit until launch completes: session=%s run=%s", sessionID, info.LifecycleID)
		watch = d.claimLaunchWatch(sessionID)
	})
	if !queued {
		return false
	}
	exit := d.holdExit(sessionID, info)
	watch.settle(launchOutcome{exit: d.exitScreenOrBare(sessionID, exit)})
	return true
}

func (r pendingPluginReport) runID() string {
	switch {
	case r.State != nil:
		return r.State.RunID
	case r.Stop != nil:
		return r.Stop.RunID
	case r.Metadata != nil:
		return r.Metadata.RunID
	case r.Transcript != nil:
		return r.Transcript.RunID
	case r.Title != nil:
		return r.Title.RunID
	case r.Prompt != nil:
		return r.Prompt.RunID
	default:
		return ""
	}
}
