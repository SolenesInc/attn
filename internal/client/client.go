package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/selfupdate"
)

func DefaultSocketPath() string {
	return config.SocketPath()
}

type Client struct {
	gardenProfile  string
	gardenSession  protocol.SessionID
	callerTerminal protocol.TerminalID
	socketPath     string
	dial           func() (net.Conn, error)
}

type automationResult struct {
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

func (c *Client) sendAutomation(msg any, out any) error {
	if c.gardenProfile != "" || c.gardenSession != "" {
		raw, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if fields["source_session_id"] == nil {
			fields["source_session_id"] = c.gardenSession
		}
		if fields["profile_id"] == nil {
			fields["profile_id"] = c.gardenProfile
		}
		msg = fields
	}
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := c.writeFrame(conn, msg); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := json.NewDecoder(conn).Decode(&raw); err != nil {
		return err
	}
	var probe automationResult
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	noteReply(probe.Success, nil)
	if !probe.Success {
		return fmt.Errorf("daemon error: %s", protocol.Deref(probe.Error))
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) AutomationApply(raw string) (*protocol.AutomationApplyResultMessage, error) {
	return c.AutomationApplyWithNamedDesktop(raw, nil, nil)
}
func (c *Client) AutomationApplyWithNamedDesktop(raw string, desktop, name *string) (*protocol.AutomationApplyResultMessage, error) {
	var result protocol.AutomationApplyResultMessage
	if err := c.sendAutomation(protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, DefinitionYaml: raw, LaunchDesktop: desktop, LaunchDesktopName: name}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationValidate(raw string) error {
	var result protocol.AutomationValidateResultMessage
	return c.sendAutomation(protocol.AutomationValidateMessage{Cmd: protocol.CmdAutomationValidate, DefinitionYaml: raw}, &result)
}

func (c *Client) AutomationDefinitions() (*protocol.AutomationDefinitionsResultMessage, error) {
	var result protocol.AutomationDefinitionsResultMessage
	if err := c.sendAutomation(protocol.AutomationDefinitionsGetMessage{Cmd: protocol.CmdAutomationDefinitionsGet}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationDefinition(id int) (*protocol.AutomationDefinitionResultMessage, error) {
	var result protocol.AutomationDefinitionResultMessage
	if err := c.sendAutomation(protocol.AutomationDefinitionGetMessage{Cmd: protocol.CmdAutomationDefinitionGet, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRun(id int, requestID, input string) (*protocol.AutomationRunResultMessage, error) {
	var result protocol.AutomationRunResultMessage
	if err := c.sendAutomation(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: id, RequestID: requestID, InputJson: protocol.Ptr(input)}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRunPullRequest(id int, requestID, prURL string) (*protocol.AutomationRunResultMessage, error) {
	var result protocol.AutomationRunResultMessage
	if err := c.sendAutomation(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: id, RequestID: requestID, PRURL: protocol.Ptr(prURL)}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationRuns(id int) (*protocol.AutomationRunsResultMessage, error) {
	var result protocol.AutomationRunsResultMessage
	if err := c.sendAutomation(protocol.AutomationRunsGetMessage{Cmd: protocol.CmdAutomationRunsGet, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationSetEnabled(id int, enabled bool) (*protocol.AutomationSetEnabledResultMessage, error) {
	var result protocol.AutomationSetEnabledResultMessage
	if err := c.sendAutomation(protocol.AutomationSetEnabledMessage{Cmd: protocol.CmdAutomationSetEnabled, DefinitionID: id, Enabled: enabled}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AutomationDelete(id int) error {
	var result protocol.AutomationDeleteResultMessage
	return c.sendAutomation(protocol.AutomationDeleteMessage{Cmd: protocol.CmdAutomationDelete, DefinitionID: id}, &result)
}

func (c *Client) AutomationCleanup(id int) (*protocol.AutomationCleanupResultMessage, error) {
	var result protocol.AutomationCleanupResultMessage
	if err := c.sendAutomation(protocol.AutomationCleanupMessage{Cmd: protocol.CmdAutomationCleanup, DefinitionID: id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type ListResult struct {
	Sessions []protocol.Session `json:"sessions"`
	Profiles []protocol.Profile `json:"profiles"`
}

func New(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return NewWithDial(socketPath, func() (net.Conn, error) {
		return net.Dial("unix", socketPath)
	})
}

func NewWithDial(socketPath string, dial func() (net.Conn, error)) *Client {
	return &Client{socketPath: socketPath, dial: dial, callerTerminal: protocol.TrimID(harness.TerminalFromEnv())}
}

func (c *Client) writeFrame(conn net.Conn, msg any) error {
	frame, err := c.marshalFrame(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(frame)
	return err
}

func (c *Client) marshalFrame(msg any) ([]byte, error) {
	frame, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var named struct {
		protocol.SocketCaller
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(frame, &named); err != nil {
		return nil, err
	}
	if c.callerTerminal != "" && named.CallerID == nil {
		caller, err := json.Marshal(protocol.SocketCaller{CallerID: &c.callerTerminal})
		if err != nil {
			return nil, err
		}
		caller[0] = ','
		frame = append(frame[:len(frame)-1], caller...)
	}
	if len(frame) > protocol.MaxSocketFrameBytes {
		return nil, fmt.Errorf("%s request: max_socket_frame_bytes=%d, asked for %d bytes", named.Cmd, protocol.MaxSocketFrameBytes, len(frame))
	}
	return append(frame, '\n'), nil
}

func (c *Client) RecordAnalytics(stream, name string, session protocol.SessionID, props map[string]any) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(recordAnalyticsDeadline)); err != nil {
		return err
	}
	message := protocol.RecordAnalyticsMessage{Cmd: protocol.CmdRecordAnalytics, Stream: stream, Name: name, Props: props}
	if session != "" {
		message.SessionID = &session
	}
	if err := c.writeFrame(conn, message); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, conn)
	return err
}

func (c *Client) connect() (net.Conn, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, explainConnectError(c.socketPath, err)
	}
	return conn, nil
}

func (c *Client) WithGardenProfile(profileID string, sessionID protocol.SessionID) *Client {
	scoped := *c
	scoped.gardenProfile, scoped.gardenSession = profileID, sessionID
	return &scoped
}

func (c *Client) WithCaller(terminal protocol.TerminalID) *Client {
	scoped := *c
	scoped.callerTerminal = protocol.TrimID(terminal)
	return &scoped
}

func (c *Client) send(msg interface{}) (*protocol.Response, error) {
	if c.gardenProfile != "" || c.gardenSession != "" {
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		cmd, _ := fields["cmd"].(string)
		if strings.HasPrefix(cmd, "seed_") || cmd == protocol.CmdOpenSeed || cmd == protocol.CmdDelegateStatus || cmd == protocol.CmdDelegate || strings.HasPrefix(cmd, "crew_") || cmd == protocol.CmdAttachmentGet {
			if fields["profile_id"] == nil || fields["profile_id"] == "" {
				fields["profile_id"] = c.gardenProfile
			}
			if cmd != protocol.CmdOpenSeed && (fields["source_session_id"] == nil || fields["source_session_id"] == "") {
				fields["source_session_id"] = c.gardenSession
			}
			msg = fields
		}
	}

	frame, err := c.marshalFrame(msg)
	if err != nil {
		return nil, err
	}
	conn, err := c.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	_, writeErr := conn.Write(frame)

	var resp protocol.Response
	reply, err := readReply(conn)
	if err == nil {
		err = json.Unmarshal(reply, &resp)
	}
	if err != nil {
		if writeErr != nil {
			return nil, fmt.Errorf("send message: %w", writeErr)
		}
		return nil, fmt.Errorf("receive response: %w", err)
	}

	noteReply(resp.Ok, resp.Refusal)
	if !resp.Ok {
		refused := &DaemonError{
			Code:     protocol.Deref(resp.ErrorCode),
			Message:  protocol.Deref(resp.Error),
			Conflict: resp.ErrorConflict,
		}
		if refusal := resp.CommandRefusal; refusal != nil && refused.Message == "" {
			refused.Code, refused.Message = refusal.Code, refusal.Message
		}
		return nil, refused
	}

	return &resp, nil
}

const recordAnalyticsDeadline = 2 * time.Second

var lastRefusal atomic.Pointer[protocol.Refusal]

func noteReply(ok bool, refusal *protocol.Refusal) {
	if ok {
		lastRefusal.Store(nil)
		return
	}
	if refusal == nil {
		refusal = protocol.Ptr(protocol.RefusalUnclassified)
	}
	lastRefusal.Store(refusal)
}

func LastRefusal() (protocol.Refusal, bool) {
	refusal := lastRefusal.Load()
	if refusal == nil {
		return "", false
	}
	return *refusal, true
}

type DaemonError struct {
	Code     string
	Message  string
	Conflict *protocol.DocumentConflict
}

func (e *DaemonError) Error() string { return fmt.Sprintf("daemon error: %s", e.Message) }

func ErrorCode(err error) string {
	var daemonErr *DaemonError
	if errors.As(err, &daemonErr) {
		return daemonErr.Code
	}
	return ""
}

func (c *Client) Unregister(id protocol.SessionID) error {
	msg := protocol.UnregisterMessage{
		Cmd: protocol.CmdUnregister,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) UpdateState(id protocol.TerminalID, state string) error {
	return c.UpdateStateFromHook(id, state, "")
}

func (c *Client) UpdateStateFromHook(id protocol.TerminalID, state string, permissionMode string) error {
	return c.UpdateStateFromHookEvidence(id, state, permissionMode, "", "")
}

func (c *Client) UpdateStateFromHookEvidence(id protocol.TerminalID, state string, permissionMode string, hookEvent string, prompt string) error {
	msg := protocol.StateMessage{
		Cmd:   protocol.CmdState,
		ID:    id,
		State: state,
	}
	if strings.TrimSpace(permissionMode) != "" {
		msg.PermissionMode = protocol.Ptr(permissionMode)
	}
	if strings.TrimSpace(hookEvent) != "" {
		msg.HookEvent = protocol.Ptr(strings.TrimSpace(hookEvent))
	}
	if strings.TrimSpace(prompt) != "" {
		msg.Prompt = protocol.Ptr(prompt)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordNotification(id protocol.TerminalID, notificationType string, message string) error {
	msg := protocol.HookNotificationMessage{
		Cmd:              protocol.CmdHookNotification,
		ID:               id,
		NotificationType: notificationType,
	}
	if strings.TrimSpace(message) != "" {
		msg.Message = protocol.Ptr(message)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordStopFailure(id protocol.TerminalID, errorType string, message string) error {
	msg := protocol.HookStopFailureMessage{
		Cmd:       protocol.CmdHookStopFailure,
		ID:        id,
		ErrorType: errorType,
	}
	if strings.TrimSpace(message) != "" {
		msg.ErrorMessage = protocol.Ptr(message)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordCompaction(id protocol.TerminalID, active bool, trigger string) error {
	msg := protocol.HookCompactionMessage{
		Cmd:    protocol.CmdHookCompaction,
		ID:     id,
		Active: active,
	}
	if strings.TrimSpace(trigger) != "" {
		msg.Trigger = protocol.Ptr(trigger)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ObserveAgentConversation(id protocol.TerminalID, nativeID string, transcriptPath string) error {
	msg := protocol.SetSessionResumeIDMessage{
		Cmd:             protocol.CmdSetSessionResumeID,
		ID:              id,
		ResumeSessionID: nativeID,
	}
	if strings.TrimSpace(transcriptPath) != "" {
		msg.TranscriptPath = protocol.Ptr(transcriptPath)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) SessionInstructions(targetSessionID string, question string) (*protocol.SessionInstructionsResult, error) {
	var result protocol.SessionInstructionsResult
	if err := c.RunCommand("session instructions", "", map[string]string{"id": targetSessionID, "question": question}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type SessionListOptions struct {
	Closed     bool
	All        bool
	Limit      int
	Before     string
	ProfileID  string
	Repository string
	Since      string
	Until      string
	Reopen     bool
}

func (c *Client) SessionList(opts SessionListOptions) (*protocol.SessionListResult, error) {
	msg := protocol.SessionListMessage{Cmd: protocol.CmdSessionList}
	if opts.Closed {
		msg.Closed = protocol.Ptr(true)
	}
	if opts.All {
		msg.All = protocol.Ptr(true)
	}
	if opts.Limit > 0 {
		msg.Limit = protocol.Ptr(opts.Limit)
	}
	if before := strings.TrimSpace(opts.Before); before != "" {
		msg.Before = protocol.Ptr(before)
	}
	if profileID := strings.TrimSpace(opts.ProfileID); profileID != "" {
		msg.ProfileID = protocol.Ptr(profileID)
	}
	if repository := strings.TrimSpace(opts.Repository); repository != "" {
		msg.Repository = protocol.Ptr(repository)
	}
	if since := strings.TrimSpace(opts.Since); since != "" {
		msg.Since = protocol.Ptr(since)
	}
	if until := strings.TrimSpace(opts.Until); until != "" {
		msg.Until = protocol.Ptr(until)
	}
	if opts.Reopen {
		msg.Reopen = protocol.Ptr(true)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SessionListResult == nil {
		return nil, errors.New("daemon returned no session list result")
	}
	return resp.SessionListResult, nil
}

func (c *Client) SessionShow(sessionID protocol.SessionID) (*protocol.SessionShowResult, error) {
	resp, err := c.send(protocol.SessionShowMessage{
		Cmd:       protocol.CmdSessionShow,
		SessionID: protocol.TrimID(sessionID),
	})
	if err != nil {
		return nil, err
	}
	if resp.SessionShowResult == nil {
		return nil, errors.New("daemon returned no session show result")
	}
	return resp.SessionShowResult, nil
}

func (c *Client) UpdateStatus() (*protocol.UpdateStatus, error) {
	resp, err := c.send(protocol.UpdateStatusMessage{Cmd: protocol.CmdUpdateStatus})
	if err != nil {
		return nil, err
	}
	if resp.UpdateStatus == nil {
		return nil, errors.New("daemon returned no update status")
	}
	return resp.UpdateStatus, nil
}

func (c *Client) RequestUpdate(caller protocol.SessionID, profile, reason string) (*protocol.UpdateRequestResult, error) {
	msg := protocol.UpdateRequestMessage{Cmd: protocol.CmdUpdateRequest}
	if profile != "" {
		msg.ProfileID = protocol.Ptr(profile)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if caller != "" {
		msg.CallerSessionID = protocol.Ptr(string(caller))
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.UpdateRequestResult == nil {
		return nil, errors.New("daemon returned no update request result")
	}
	return resp.UpdateRequestResult, nil
}

func (c *Client) RenameSession(sessionID protocol.SessionID, name string) error {
	_, err := c.send(protocol.RenameSessionMessage{
		Cmd:       protocol.CmdRenameSession,
		SessionID: protocol.TrimID(sessionID),
		Label:     strings.TrimSpace(name),
	})
	return err
}

type SessionReopenOptions struct {
	SessionID protocol.SessionID
	Action    string
}

func (c *Client) SessionReopen(opts SessionReopenOptions) (*protocol.SessionReopenResult, error) {
	msg := protocol.SessionReopenMessage{
		Cmd:       protocol.CmdSessionReopen,
		SessionID: protocol.TrimID(opts.SessionID),
	}
	if action := strings.TrimSpace(opts.Action); action != "" {
		msg.Action = protocol.Ptr(protocol.SessionReopenAction(action))
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.SessionReopenResult == nil {
		return nil, errors.New("daemon returned no session reopen result")
	}
	return resp.SessionReopenResult, nil
}

func (c *Client) SessionTranscript(targetSessionID string, afterCursor string) (*protocol.SessionTranscriptResult, error) {
	args := map[string]string{"id": targetSessionID}
	if after := strings.TrimSpace(afterCursor); after != "" {
		args["after"] = after
	}
	var result protocol.SessionTranscriptResult
	if err := c.RunCommand("session transcript", "", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) StateExplain(targetSessionID string) (*protocol.StateExplainResult, error) {
	var result protocol.StateExplainResult
	if err := c.RunCommand("state explain", "", map[string]string{"id": targetSessionID}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentPeek(targetSessionID string) (*protocol.AgentPeekResult, error) {
	var result protocol.AgentPeekResult
	if err := c.RunCommand("agent peek", "", map[string]any{"target": targetSessionID}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentMsg(target string, sourceSessionID protocol.SessionID, content string) (*protocol.AgentMsgResult, error) {
	var result protocol.AgentMsgResult
	if err := c.RunCommand("agent msg", sourceSessionID, map[string]any{"target": target, "text": content}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentReload(targets []string, sourceSessionID protocol.SessionID, all bool) (*protocol.AgentReloadResult, error) {
	args := map[string]any{}
	if len(targets) > 0 {
		args["targets"] = targets
	}
	if all {
		args["all"] = true
	}
	var result protocol.AgentReloadResult
	if err := c.RunCommand("agent reload", sourceSessionID, args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentClose(target string, sourceSessionID protocol.SessionID, reason string) (*protocol.AgentCloseResult, error) {
	return c.agentClose(target, sourceSessionID, reason, false)
}

func (c *Client) AgentCloseCompacting(sessionID protocol.SessionID, reason string) (*protocol.AgentCloseResult, error) {
	return c.agentClose(string(sessionID), sessionID, reason, true)
}

func (c *Client) agentClose(target string, sourceSessionID protocol.SessionID, reason string, compact bool) (*protocol.AgentCloseResult, error) {
	args := map[string]any{"reason": reason}
	if compact {
		args["self"], args["compact"] = true, true
	} else {
		args["target"] = target
	}
	var result protocol.AgentCloseResult
	if err := c.RunCommand("agent close", sourceSessionID, args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentInbox(messageID string, recipientSessionID protocol.SessionID) (*protocol.AgentPeerMessage, error) {
	message, _, err := c.AgentInboxEntry(messageID, recipientSessionID)
	if err == nil && message == nil {
		err = errors.New("daemon returned no peer message")
	}
	return message, err
}

func (c *Client) AgentInboxBatch(recipientSessionID protocol.SessionID, limit int) (*protocol.AgentInboxBatchResult, error) {
	args := map[string]any{}
	if limit != 0 {
		args["limit"] = limit
	}
	var result protocol.AgentInboxBatchResult
	if err := c.RunCommand("agent inbox", recipientSessionID, args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentMsgStatus(messageID string, senderSessionID protocol.SessionID) (*protocol.AgentPeerMessage, error) {
	var result protocol.AgentPeerMessage
	if err := c.RunCommand("agent msg-status", senderSessionID, map[string]any{"message": messageID}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type StopFacts struct {
	BackgroundTasks     []protocol.StopBackgroundTask
	PendingSessionCrons int
}

func (c *Client) SendStop(id protocol.TerminalID, transcriptPath string, facts StopFacts) error {
	msg := protocol.StopMessage{
		Cmd:                 protocol.CmdStop,
		ID:                  id,
		TranscriptPath:      transcriptPath,
		BackgroundTasks:     facts.BackgroundTasks,
		PendingSessionCrons: protocol.Ptr(facts.PendingSessionCrons),
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordFilesEdited(id protocol.TerminalID, paths []string) error {
	msg := protocol.FilesEditedMessage{
		Cmd:   protocol.CmdFilesEdited,
		ID:    id,
		Paths: paths,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) RecordPullRequestCreated(id protocol.SessionID, url string) error {
	msg := protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated,
		ID:  id,
		URL: url,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ForgetSessionPullRequest(id protocol.SessionID, url string) error {
	var result protocol.PullRequestResult
	return c.RunCommand("pr forget", id, command.PRForgetArgs{URL: url}, &result)
}

func (c *Client) WatchSessionPullRequest(id protocol.SessionID, seedID, url string, mode protocol.PullRequestWatchMode, reviewer string) error {
	var result protocol.PullRequestWatchResult
	return c.RunCommand("pr watch", id, command.PRWatchArgs{URL: url, Seed: seedID, Mode: string(mode), Reviewer: reviewer}, &result)
}

func (c *Client) UnwatchSessionPullRequest(id protocol.SessionID, seedID, url string) error {
	var result protocol.PullRequestResult
	return c.RunCommand("pr unwatch", id, command.PRUnwatchArgs{URL: url, Seed: seedID}, &result)
}

func (c *Client) PullRequests(id protocol.SessionID, seedID string) ([]protocol.SessionPullRequest, error) {
	var result protocol.PullRequestListResult
	if err := c.RunCommand("pr ls", id, command.PRLsArgs{Seed: seedID}, &result); err != nil {
		return nil, err
	}
	return result.PullRequests, nil
}

func (c *Client) StartDelegation(msg protocol.DelegateMessage) (*protocol.DelegationOperation, error) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	msg.Cmd = protocol.CmdDelegate
	msg.RequestID = requestID
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.DelegationOperation == nil {
		return nil, errors.New("daemon returned no delegation operation")
	}
	return resp.DelegationOperation, nil
}

func (c *Client) DelegationStatus(id string) (*protocol.DelegationOperation, error) {
	return c.delegationStatus(protocol.DelegateStatusMessage{Cmd: protocol.CmdDelegateStatus, ID: strings.TrimSpace(id)})
}

func (c *Client) AwaitDelegation(op *protocol.DelegationOperation, changed func(*protocol.DelegationOperation)) (*protocol.DelegationOperation, error) {
	for op.State == protocol.DelegationOperationStateAccepted || op.State == protocol.DelegationOperationStatePreparing {
		changed(op)
		next, err := c.delegationStatus(protocol.DelegateStatusMessage{
			Cmd: protocol.CmdDelegateStatus, ID: op.OperationID, WaitPast: protocol.Ptr(op.UpdatedAt),
		})
		if err != nil {
			return nil, err
		}
		if next.UpdatedAt == op.UpdatedAt && next.State == op.State {
			return nil, errors.New("the running daemon answered delegate_status without waiting, so it predates this attn; restart it with `attn daemon ensure`")
		}
		op = next
	}
	return op, nil
}

func (c *Client) delegationStatus(msg protocol.DelegateStatusMessage) (*protocol.DelegationOperation, error) {
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.DelegationOperation == nil {
		return nil, errors.New("daemon returned no delegation operation")
	}
	return resp.DelegationOperation, nil
}

func (c *Client) Delegate(request protocol.DelegateMessage) (*protocol.DelegateResult, error) {
	profileID, sessionID := c.gardenProfile, c.gardenSession
	if request.ProfileID != nil {
		profileID = *request.ProfileID
	}
	if request.SourceSessionID != nil {
		sessionID = *request.SourceSessionID
	}
	c = c.WithGardenProfile(profileID, sessionID)
	op, err := c.StartDelegation(request)
	if err != nil {
		return nil, err
	}
	op, err = c.AwaitDelegation(op, func(*protocol.DelegationOperation) {})
	if err != nil {
		return nil, err
	}
	if op.State == protocol.DelegationOperationStateFailed {
		message := "unknown failure"
		if op.Failure != nil {
			message = op.Failure.Message
		}
		return nil, fmt.Errorf("delegation failed: %s", message)
	}
	if op.Result == nil {
		return nil, errors.New("completed delegation has no result")
	}
	return op.Result, nil
}

func (c *Client) AskOpen(sessionID protocol.SessionID, seedID, markdown, subject, baseDir string, images ...protocol.AskImage) (*protocol.Ask, error) {
	msg := protocol.AskOpenMessage{Cmd: protocol.CmdAskOpen, SessionID: sessionID, Markdown: markdown, BaseDir: baseDir, Images: images}
	if seedID = strings.TrimSpace(seedID); seedID != "" {
		msg.SeedID = protocol.Ptr(seedID)
	}
	if subject = strings.TrimSpace(subject); subject != "" {
		msg.Subject = protocol.Ptr(subject)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if resp.AskOpenResult == nil {
		return nil, errors.New("daemon returned no ask")
	}
	return resp.AskOpenResult, nil
}

func (c *Client) AskList(sessionID protocol.SessionID) (*protocol.AskListResult, error) {
	var result protocol.AskListResult
	if err := c.RunCommand("ask list", sessionID, struct{}{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentTurn(sessionID protocol.SessionID, action protocol.AgentTurnAction) (*protocol.AgentTurnResult, error) {
	var result protocol.AgentTurnResult
	if err := c.RunCommand("turn "+string(action), sessionID, struct{}{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) RecordVent(sessionID protocol.SessionID, kind protocol.VentKind, text string) (*protocol.Vent, error) {
	var vent protocol.Vent
	if err := c.RunCommand("vent", sessionID, map[string]string{"about": string(kind), "text": text}, &vent); err != nil {
		return nil, err
	}
	return &vent, nil
}

func (c *Client) WithdrawVent(sessionID protocol.SessionID, ventID string) error {
	return c.RunCommand("vent withdraw", sessionID, map[string]string{"vent": ventID}, &protocol.VentWithdrawResult{})
}

func (c *Client) AskShow(sessionID protocol.SessionID, askID string) (*protocol.AskShowResult, error) {
	var result protocol.AskShowResult
	if err := c.RunCommand("ask show", sessionID, map[string]string{"ask": askID}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AskClose(sessionID protocol.SessionID, askID, reason string) error {
	args := map[string]string{"ask": askID}
	if reason = strings.TrimSpace(reason); reason != "" {
		args["reason"] = reason
	}
	return c.RunCommand("ask close", sessionID, args, &protocol.AskCloseResult{})
}

func (c *Client) AppendJournal(sourceSessionID protocol.SessionID, date string, entry string) (*protocol.JournalAppendResult, error) {
	return commandResult[protocol.JournalAppendResult](c, "journal append", sourceSessionID, command.JournalAppendArgs{Entry: entry, Date: date})
}

func (c *Client) ActivityStatus() (*protocol.ActivityStatusResult, error) {
	var result protocol.ActivityStatusResult
	if err := c.RunCommand("activity", "", map[string]string{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ClearSessionActivity(sessionID protocol.SessionID) error {
	var cleared string
	return c.RunCommand("activity clear", "", map[string]string{"id": string(sessionID)}, &cleared)
}

func (c *Client) queryResponse(filter string) (*protocol.Response, error) {
	var filterPtr *string
	if filter != "" {
		filterPtr = &filter
	}
	msg := protocol.QueryMessage{
		Cmd:    protocol.CmdQuery,
		Filter: filterPtr,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CallerSession names the session the terminal shows, or "" when it shows none.
func (c *Client) CallerSession(terminal protocol.TerminalID) (protocol.SessionID, error) {
	resp, err := c.send(protocol.CallerSessionMessage{Cmd: protocol.CmdCallerSession, CallerID: &terminal})
	if err != nil {
		return "", err
	}
	return protocol.Deref(resp.CallerSessionID), nil
}

func (c *Client) Query(filter string) ([]protocol.Session, error) {
	resp, err := c.queryResponse(filter)
	if err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (c *Client) List(filter string) (*ListResult, error) {
	resp, err := c.queryResponse(filter)
	if err != nil {
		return nil, err
	}
	sessions := resp.Sessions
	if sessions == nil {
		sessions = []protocol.Session{}
	}
	profiles := resp.Profiles
	if profiles == nil {
		profiles = []protocol.Profile{}
	}
	return &ListResult{
		Sessions: sessions,
		Profiles: profiles,
	}, nil
}

// OpenMarkdown answers the problems of a review document, as review check lists them.
func (c *Client) OpenMarkdown(path string, sessionID protocol.SessionID) ([]protocol.ReviewProblem, error) {
	msg := protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: path,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.ReviewProblems, nil
}

func (c *Client) ReviewStart(document, repository, base string) (*protocol.ReviewStartResult, error) {
	return commandResult[protocol.ReviewStartResult](c, "review start", "", map[string]any{"document": document, "repository": repository, "base": base})
}

func (c *Client) ReviewCheck(document string) ([]protocol.ReviewProblem, error) {
	result, err := commandResult[protocol.ReviewCheckResult](c, "review check", "", map[string]any{"document": document})
	if err != nil {
		return nil, err
	}
	return result.Problems, nil
}

func (c *Client) ReviewAnswer(document, annotationID, text string) error {
	_, err := commandResult[protocol.ReviewAnswerResult](c, "review answer", "", map[string]any{"document": document, "question": annotationID, "text": text})
	return err
}

func (c *Client) CodeNavCheck(repository, path string, line int) (*protocol.CodeNavCheckResult, error) {
	return commandResult[protocol.CodeNavCheckResult](c, "nav check", "", map[string]any{"repository": repository, "at": fmt.Sprintf("%s:%d", path, line), "wait": true})
}

func (c *Client) CodeNavStatus(repository string) (*protocol.CodeNavStatusResult, error) {
	return commandResult[protocol.CodeNavStatusResult](c, "nav status", "", map[string]any{"repository": repository})
}

func (c *Client) CodeNavLink(target string) (*protocol.CodeNavLinkResult, error) {
	return commandResult[protocol.CodeNavLinkResult](c, "nav link", "", map[string]any{"program": target})
}

func (c *Client) CodeNavUnlink(name string) error {
	_, err := commandResult[protocol.CodeNavLinkResult](c, "nav unlink", "", map[string]any{"name": name})
	return err
}

func (c *Client) CodeNavDefine(repository, language, definition string) (*protocol.CodeNavDefineResult, error) {
	if definition == "" {
		return commandResult[protocol.CodeNavDefineResult](c, "nav rm", "", map[string]any{"repository": repository, "language": language})
	}
	return commandResult[protocol.CodeNavDefineResult](c, "nav set", "", map[string]any{"repository": repository, "language": language, "definition": definition})
}

func (c *Client) OpenSeed(seedID string, sessionID protocol.SessionID) error {
	msg := protocol.OpenSeedMessage{Cmd: protocol.CmdOpenSeed, SeedID: seedID}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenSentFiles(sessionID protocol.SessionID, paths []string) error {
	msg := protocol.OpenSentFilesMessage{
		Cmd:   protocol.CmdOpenSentFiles,
		Paths: paths,
	}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) OpenBrowser(url string, sessionID protocol.SessionID) error {
	_, err := commandResult[protocol.BrowserOpenResult](c, "browser open", sessionID, map[string]any{"url": url})
	return err
}

func (c *Client) CalloutShow(sessionID protocol.SessionID, steps []protocol.CalloutShowStep) (*protocol.CalloutShowResult, error) {
	return commandResult[protocol.CalloutShowResult](c, "callout show", sessionID, map[string]any{"steps": steps})
}

func (c *Client) CalloutAnchors(sessionID protocol.SessionID) (*protocol.CalloutAnchorsResult, error) {
	return commandResult[protocol.CalloutAnchorsResult](c, "callout anchors", sessionID, map[string]any{})
}

func commandResult[R any](c *Client, name string, session protocol.SessionID, args any) (*R, error) {
	var result R
	if err := c.RunCommand(name, session, args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) BrowserCommand(action string, params string, sessionID protocol.SessionID) (string, error) {
	result, err := commandResult[protocol.BrowserResult](c, "browser command", sessionID, map[string]any{"action": action, "params": params, "wait": true})
	if err != nil {
		return "", err
	}
	return result.Data, nil
}

func (c *Client) BrowserType(selector, text string, sessionID protocol.SessionID) (string, error) {
	result, err := commandResult[protocol.BrowserResult](c, "browser type", sessionID, map[string]any{"selector": selector, "text": text})
	if err != nil {
		return "", err
	}
	return result.Data, nil
}

func (c *Client) QueryPRs(filter string) ([]protocol.PR, error) {
	var filterPtr *string
	if filter != "" {
		filterPtr = &filter
	}
	msg := protocol.QueryPRsMessage{
		Cmd:    protocol.CmdQueryPRs,
		Filter: filterPtr,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Prs, nil
}

func (c *Client) ToggleMutePR(id string) error {
	msg := protocol.MutePRMessage{
		Cmd: protocol.CmdMutePR,
		ID:  id,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) ToggleMuteRepo(repo string) error {
	msg := map[string]string{
		"cmd":  protocol.CmdMuteRepo,
		"repo": repo,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) SetRepoCollapsed(repo string, collapsed bool) error {
	msg := map[string]interface{}{
		"cmd":       protocol.CmdCollapseRepo,
		"repo":      repo,
		"collapsed": collapsed,
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) QueryRepos() ([]protocol.RepoState, error) {
	msg := map[string]string{
		"cmd": protocol.CmdQueryRepos,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Repos, nil
}

func (c *Client) QueryAuthors() ([]protocol.AuthorState, error) {
	msg := map[string]string{
		"cmd": protocol.CmdQueryAuthors,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Authors, nil
}

func (c *Client) FetchPRDetails(id string) ([]protocol.PR, error) {
	msg := protocol.FetchPRDetailsMessage{
		Cmd: protocol.CmdFetchPRDetails,
		ID:  id,
	}
	resp, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	return resp.Prs, nil
}

func explainConnectError(sockPath string, cause error) error {
	instance := config.InstanceLabel()
	if errors.Is(cause, syscall.EPERM) {
		return fmt.Errorf("the sandbox blocks attn's socket at %s (instance=%s): %w\n  run this command with escalated permissions; in Codex, allow its prefix in ~/.codex/rules/default.rules for future runs", config.CollapseHome(sockPath), instance, cause)
	}
	base := fmt.Sprintf("connect to daemon at %s (instance=%s): %v",
		config.CollapseHome(sockPath), instance, cause)
	if restarting := selfupdate.RestartMarkerText(config.DataDir(), time.Now()); restarting != "" {
		return errors.New(base + "\n  " + restarting)
	}
	if hint := crossInstanceHint(); hint != "" {
		return errors.New(base + "\n  " + hint)
	}
	return errors.New(base)
}

func crossInstanceHint() string {
	current := config.Instance()
	if current == "" {
		otherSock := config.SocketPathForInstance("dev")
		if socketLive(otherSock) {
			return fmt.Sprintf("hint: a dev daemon is listening at %s — run `eval \"$(attn instance-env dev)\"` to switch this shell",
				config.CollapseHome(otherSock))
		}
		return ""
	}
	otherSock := config.SocketPathForInstance("")
	if socketLive(otherSock) {
		return fmt.Sprintf("hint: the default daemon is listening at %s — run `eval \"$(attn instance-env --unset)\"` to switch this shell",
			config.CollapseHome(otherSock))
	}
	return ""
}

func socketLive(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *Client) SetAutomationLaunchDesktop(id int, desktop string, name *string) (*protocol.LaunchDesktopResultMessage, error) {
	var result protocol.LaunchDesktopResultMessage
	msg := protocol.LaunchDesktopSetMessage{Cmd: protocol.CmdLaunchDesktopSet, Kind: protocol.LaunchDesktopKindAutomation, ItemID: strconv.Itoa(id), DesktopRef: &desktop, DesktopName: name}
	if err := c.sendAutomation(msg, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SnoozeSession(callerID, sessionID protocol.SessionID, until time.Time) error {
	msg := protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: protocol.TrimID(sessionID), Until: until.UTC().Format(time.RFC3339Nano)}
	if caller := protocol.TrimID(callerID); caller != "" {
		msg.CallerSessionID = protocol.Ptr(caller)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) UnsnoozeSession(callerID, sessionID protocol.SessionID) error {
	msg := protocol.WakeTurnMessage{Cmd: protocol.CmdWakeTurn, SessionID: protocol.TrimID(sessionID)}
	if caller := protocol.TrimID(callerID); caller != "" {
		msg.CallerSessionID = protocol.Ptr(caller)
	}
	_, err := c.send(msg)
	return err
}

func (c *Client) SetSessionPriority(sessionID protocol.SessionID, priority bool) error {
	_, err := c.send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: protocol.TrimID(sessionID), Priority: priority})
	return err
}
func (c *Client) OpenSentFilesFromTerminal(terminalID protocol.TerminalID, paths []string) error {
	msg := protocol.OpenSentFilesMessage{Cmd: protocol.CmdOpenSentFiles, TerminalID: protocol.Ptr(terminalID), Paths: paths}
	_, err := c.send(msg)
	return err
}
func (c *Client) RecordPullRequestCreatedFromTerminal(terminalID protocol.TerminalID, url string) error {
	msg := protocol.PullRequestCreatedMessage{Cmd: protocol.CmdPullRequestCreated, TerminalID: protocol.Ptr(terminalID), URL: url}
	_, err := c.send(msg)
	return err
}
