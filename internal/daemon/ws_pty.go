package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

var wsSubscriberCounter atomic.Int64

const endedStreamProbeTimeout = 10 * time.Second

func wsSubscriberID(client *wsClient, terminalID protocol.TerminalID) string {
	n := wsSubscriberCounter.Add(1)
	return fmt.Sprintf("%p:%s:%d", client, terminalID, n)
}

type attachReplayPayload struct {
	ghosttySnapshot       []byte
	ghosttySnapshotFormat string
	ghosttyCols           uint16
	ghosttyRows           uint16
	ghosttyBlocks         []pty.AttachBlockData
	ghosttyPlacements     []pty.KittyPlacement
	scrollbackTruncated   bool
	decision              string
}

func shouldIncludeAttachReplay(policy protocol.AttachPolicy) bool {
	return policy != protocol.AttachPolicyFreshSpawn
}

func buildAttachReplayPayload(info ptybackend.AttachInfo, policy protocol.AttachPolicy) attachReplayPayload {
	if !shouldIncludeAttachReplay(policy) {
		return attachReplayPayload{decision: "omit_replay_for_policy"}
	}
	if len(info.GhosttySnapshot) == 0 {
		return attachReplayPayload{decision: "no_snapshot"}
	}
	return attachReplayPayload{
		ghosttySnapshot:       info.GhosttySnapshot,
		ghosttySnapshotFormat: info.GhosttySnapshotFormat,
		ghosttyCols:           info.Cols,
		ghosttyRows:           info.Rows,
		ghosttyBlocks:         info.GhosttyBlocks,
		ghosttyPlacements:     info.GhosttyPlacements,
		scrollbackTruncated:   info.GhosttyScrollbackTruncated,
		decision:              "use_ghostty_snapshot",
	}
}

func (d *Daemon) detachSession(client *wsClient, terminalID protocol.TerminalID) {
	client.attachMu.Lock()
	stream, hasStream := client.attachedStreams[terminalID]
	if hasStream {
		delete(client.attachedStreams, terminalID)
	}
	client.attachMu.Unlock()
	if hasStream {
		_ = stream.Close()
	}
}

func (c *wsClient) streamsTo(terminal protocol.TerminalID) bool {
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	_, attached := c.attachedStreams[terminal]
	return attached
}

type terminalCommand struct {
	run    func()
	inputs []*protocol.PtyInputMessage
}

type terminalOrder struct {
	mu        sync.Mutex
	queued    map[protocol.TerminalID][]*terminalCommand
	lastInput *terminalCommand
}

func (o *terminalOrder) breakInputBatch() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastInput = nil
}

func (o *terminalOrder) queueBehindRunning(terminal protocol.TerminalID, command func()) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastInput = nil
	queue, running := o.queued[terminal]
	if running {
		o.queued[terminal] = append(queue, &terminalCommand{run: command})
	}
	return running
}

func (o *terminalOrder) queue(terminal protocol.TerminalID, command func()) (startRunning bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.queued == nil {
		o.queued = make(map[protocol.TerminalID][]*terminalCommand)
	}
	o.lastInput = nil
	queue, running := o.queued[terminal]
	o.queued[terminal] = append(queue, &terminalCommand{run: command})
	return !running
}

func (o *terminalOrder) queueInput(msg *protocol.PtyInputMessage, write func([]*protocol.PtyInputMessage)) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.queued == nil {
		o.queued = make(map[protocol.TerminalID][]*terminalCommand)
	}
	queue, running := o.queued[msg.ID]
	if len(queue) > 0 {
		last := queue[len(queue)-1]
		if last == o.lastInput && strings.TrimSpace(protocol.Deref(last.inputs[0].Source)) == strings.TrimSpace(protocol.Deref(msg.Source)) {
			last.inputs = append(last.inputs, msg)
			return false
		}
	}
	command := &terminalCommand{inputs: []*protocol.PtyInputMessage{msg}}
	command.run = func() { write(command.inputs) }
	o.lastInput = command
	o.queued[msg.ID] = append(queue, command)
	return !running
}

func (o *terminalOrder) runQueued(terminal protocol.TerminalID) {
	for {
		o.mu.Lock()
		queue := o.queued[terminal]
		if len(queue) == 0 {
			delete(o.queued, terminal)
			o.mu.Unlock()
			return
		}
		next := queue[0]
		if o.lastInput == next {
			o.lastInput = nil
		}
		o.queued[terminal] = queue[1:]
		o.mu.Unlock()
		next.run()
	}
}

func (d *Daemon) inTerminalOrder(client *wsClient, terminal protocol.TerminalID, command func()) {
	if d.sessionHost(d.terminals().owner(terminal)) != "" {
		d.offPumpInTerminalOrder(client, terminal, command)
		return
	}
	if !client.terminalOrder.queueBehindRunning(terminal, command) {
		command()
	}
}

func (d *Daemon) offPumpInTerminalOrder(client *wsClient, terminal protocol.TerminalID, command func()) {
	if !client.terminalOrder.queue(terminal, command) {
		return
	}
	d.runTerminalOrder(client, terminal)
}

func (d *Daemon) runTerminalOrder(client *wsClient, terminal protocol.TerminalID) {
	if !d.life.Go("terminalOrder", func() { client.terminalOrder.runQueued(terminal) }) {
		client.terminalOrder.runQueued(terminal)
	}
}

func (d *Daemon) detachAllSessions(client *wsClient) {
	client.attachMu.Lock()
	streams := make([]ptybackend.Stream, 0, len(client.attachedStreams))
	for _, stream := range client.attachedStreams {
		streams = append(streams, stream)
	}
	client.attachedStreams = make(map[protocol.TerminalID]ptybackend.Stream)
	client.attachMu.Unlock()
	for _, stream := range streams {
		_ = stream.Close()
	}
}

func normalizeSpawnAgent(raw string) string {
	if agent := strings.TrimSpace(strings.ToLower(raw)); agent != "" {
		return agent
	}
	return string(protocol.SessionAgentCodex)
}

func legacyExecutableFromSpawnMessage(msg *protocol.SpawnSessionMessage, agent string) string {
	switch strings.TrimSpace(strings.ToLower(agent)) {
	case string(protocol.SessionAgentClaude):
		return strings.TrimSpace(protocol.Deref(msg.ClaudeExecutable))
	case string(protocol.SessionAgentCodex):
		return strings.TrimSpace(protocol.Deref(msg.CodexExecutable))
	default:
		return ""
	}
}

func namesHome(cwd string) bool {
	trimmed := strings.TrimSpace(cwd)
	return trimmed == "~" || strings.HasPrefix(trimmed, "~/")
}

func expandHome(cwd, home string) string {
	if home == "" || !namesHome(cwd) {
		return cwd
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimSpace(cwd), "~"))
}

func (d *Daemon) sendSpawnFailure(client *wsClient, sessionID protocol.SessionID, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if strings.TrimSpace(errMsg) == "" {
		errMsg = "spawn failed"
	}
	d.sendToClient(client, protocol.SpawnResultMessage{
		Event:   protocol.EventSpawnResult,
		ID:      sessionID,
		Success: false,
		Error:   protocol.Ptr(errMsg),
	})
}

func buildSpawnSessionRecord(msg *protocol.SpawnSessionMessage, agent, cwd, label, profileID string, existing *protocol.Session, isShell, pluginReportsNoState bool, parentSessionID protocol.SessionID, branchInfo *git.BranchInfo) *protocol.Session {
	nowStr := string(protocol.TimestampNow())
	state := protocol.SessionStateLaunching
	if isShell {
		state = protocol.SessionStateIdle
	}
	stateSince, stateUpdatedAt := nowStr, nowStr
	if existing != nil && existing.State != protocol.SessionStateRecoverable {
		state, stateSince, stateUpdatedAt = existing.State, existing.StateSince, existing.StateUpdatedAt
		if stateSince == "" {
			stateSince = nowStr
		}
		if stateUpdatedAt == "" {
			stateUpdatedAt = nowStr
		}
	}
	if pluginReportsNoState {
		state, stateSince, stateUpdatedAt = protocol.SessionStateWorking, nowStr, nowStr
	}
	session := &protocol.Session{ID: msg.ID, Label: label, Agent: protocol.SessionAgent(agent), Directory: cwd, State: state, StateSince: stateSince, StateUpdatedAt: stateUpdatedAt, LastSeen: nowStr, ProfileID: profileID}
	if protocol.Deref(msg.Priority) {
		session.Priority = protocol.Ptr(true)
	}
	if existing != nil {
		session.Priority = existing.Priority
	}
	if parentSessionID != "" {
		session.ParentSessionID = protocol.Ptr(parentSessionID)
	}
	applyBranchInfo(session, branchInfo)
	return session
}

func applyBranchInfo(session *protocol.Session, branchInfo *git.BranchInfo) {
	if branchInfo == nil {
		return
	}
	if branchInfo.Branch != "" {
		session.Branch = protocol.Ptr(branchInfo.Branch)
	}
	if branchInfo.IsWorktree {
		session.IsWorktree = protocol.Ptr(true)
	}
	if branchInfo.MainRepo != "" {
		session.MainRepo = protocol.Ptr(branchInfo.MainRepo)
	}
	if branchInfo.Repository != "" {
		session.Repository = protocol.Ptr(branchInfo.Repository)
	}
}

func (d *Daemon) handleSpawnSession(client *wsClient, msg *protocol.SpawnSessionMessage) {
	placed, rejection := d.runSpawnPipelineReporting(msg, internalSpawnPolicy{userStarted: true, requester: client})
	d.answerSpawn(client, msg.ID, placed, rejection)
}

func (d *Daemon) answerSpawn(client *wsClient, sessionID protocol.SessionID, placed placementOutcome, rejection *spawnRejection) {
	if rejection != nil {
		d.sendSpawnRejection(client, sessionID, rejection)
		return
	}
	result := protocol.SpawnResultMessage{Event: protocol.EventSpawnResult, ID: placed.session, Success: true}
	if placed.paneID != "" {
		result.DesktopID = protocol.Ptr(placed.desktopID)
		result.PaneID = protocol.Ptr(placed.paneID)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) sendSpawnRejection(client *wsClient, sessionID protocol.SessionID, rejection *spawnRejection) {
	if rejection.commandError != "" {
		d.sendCommandError(client, protocol.CmdSpawnSession, rejection.commandError)
		return
	}
	d.sendSpawnFailure(client, sessionID, rejection.err)
}

func buildStoredIntentSpawn(session *protocol.Session, intent store.LaunchIntent, cols, rows int) (*protocol.SpawnSessionMessage, internalSpawnPolicy) {
	spawnMsg := &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        session.ID,
		Host:      session.Host,
		Cwd:       session.Directory,
		Agent:     session.Agent,
		ProfileID: session.ProfileID,
		Label:     protocol.Ptr(session.Label),
		Cols:      cols,
		Rows:      rows,
		YoloMode:  protocol.Ptr(intent.YoloMode),
		AutoMode:  intent.AutoMode,
	}
	if intent.ApprovalPolicy != "" {
		spawnMsg.ApprovalPolicy = protocol.Ptr(intent.ApprovalPolicy)
	}
	if intent.SandboxMode != "" {
		spawnMsg.SandboxMode = protocol.Ptr(intent.SandboxMode)
	}
	if intent.Executable != "" {
		spawnMsg.Executable = protocol.Ptr(intent.Executable)
	}
	if intent.Model != "" {
		spawnMsg.Model = protocol.Ptr(intent.Model)
	}
	if intent.Effort != "" {
		spawnMsg.Effort = protocol.Ptr(intent.Effort)
	}
	launch := intent.UnattendedLaunch
	if !launch.IsZero() {
		launch = launch.WithLegacyDefaults()
		spawnMsg.Model = protocol.Ptr(launch.Model)
		spawnMsg.Effort = protocol.Ptr(launch.Effort)
		spawnMsg.Executable = protocol.Ptr(launch.Executable)
	}
	return spawnMsg, internalSpawnPolicy{
		unattendedLaunch:      launch,
		approvalRoute:         intent.ApprovalRoute,
		preserveApprovalRoute: true,
	}
}

func (d *Daemon) reviveSessionForAttach(msg *protocol.AttachSessionMessage) error {
	sessionID, _ := d.shownIn(msg.ID)
	session := d.store.Get(sessionID)
	if session == nil || session.State != protocol.SessionStateRecoverable {
		return errors.New("session not recoverable")
	}
	if msg.Cols == nil || msg.Rows == nil || *msg.Cols <= 0 || *msg.Rows <= 0 {
		return errors.New("revive requires pty geometry")
	}
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return errors.New("no stored launch intent")
	}
	spawnMsg, policy := buildStoredIntentSpawn(session, intent, int(*msg.Cols), int(*msg.Rows))
	if rejection := d.runSpawnPipeline(spawnMsg, policy); rejection != nil {
		return rejection.reason()
	}
	return nil
}

func (d *Daemon) handleAttachSession(client *wsClient, msg *protocol.AttachSessionMessage) {
	subID := wsSubscriberID(client, msg.ID)
	policy := protocol.Deref(msg.AttachPolicy)
	attachOptions := ptybackend.AttachOptions{OmitReplay: !shouldIncludeAttachReplay(policy)}

	terminal := msg.ID
	info, stream, err := d.router().Attach(context.Background(), terminal, subID, attachOptions)
	revived := false
	if err != nil && errors.Is(err, pty.ErrSessionNotFound) && protocol.Deref(msg.AttachPolicy) == protocol.AttachPolicyRevive {
		attachErr := err
		if reviveErr := d.reviveSessionForAttach(msg); reviveErr != nil {
			err = errors.Join(attachErr, reviveErr)
		} else {
			info, stream, err = d.router().Attach(context.Background(), terminal, subID, attachOptions)
			if err == nil {
				revived = true
			}
		}
	}
	if errors.Is(err, pty.ErrSessionNotFound) && policy != protocol.AttachPolicyRevive {
		if sessionID, shown := d.shownIn(terminal); shown {
			if exit := d.store.GetSessionExitScreen(sessionID); exit != nil {
				d.sendToClient(client, protocol.AttachResultMessage{
					Event:   protocol.EventAttachResult,
					ID:      terminal,
					Success: true,
					Running: protocol.Ptr(false),
					Exit:    exit.TerminalExit(),
					Screen:  &protocol.AgentPeekScreen{Text: exit.Text, Cols: exit.Cols, Rows: exit.Rows},
				})
				return
			}
		}
	}

	if err != nil {
		d.sendToClient(client, protocol.AttachResultMessage{
			Event:   protocol.EventAttachResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		})
		return
	}
	replay := buildAttachReplayPayload(info, policy)
	d.logf(
		"PTY attach result: id=%s policy=%s running=%v last_seq=%d ghostty_snapshot_bytes=%d snapshot_format=%s scrollback_truncated=%v replay_decision=%s size=%dx%d",
		msg.ID,
		policy,
		info.Running,
		info.LastSeq,
		len(replay.ghosttySnapshot),
		replay.ghosttySnapshotFormat,
		replay.scrollbackTruncated,
		replay.decision,
		info.Cols,
		info.Rows,
	)

	client.attachMu.Lock()
	previous := client.attachedStreams[msg.ID]
	client.attachedStreams[msg.ID] = stream
	client.attachMu.Unlock()
	if previous != nil && previous != stream {
		_ = previous.Close()
	}
	result := protocol.AttachResultMessage{
		Event:   protocol.EventAttachResult,
		ID:      msg.ID,
		Success: true,
		LastSeq: protocol.Ptr(int(info.LastSeq)),
		Cols:    protocol.Ptr(int(info.Cols)),
		Rows:    protocol.Ptr(int(info.Rows)),
		Pid:     protocol.Ptr(info.PID),
		Running: protocol.Ptr(info.Running),
	}
	if revived {
		result.Revived = protocol.Ptr(true)
	}
	if len(replay.ghosttySnapshot) > 0 {
		result.Snapshot = &protocol.AttachSnapshot{
			Cols:                int(replay.ghosttyCols),
			Rows:                int(replay.ghosttyRows),
			SnapshotB64:         base64.StdEncoding.EncodeToString(replay.ghosttySnapshot),
			Format:              protocol.Ptr(replay.ghosttySnapshotFormat),
			Blocks:              attachBlocksToProtocol(replay.ghosttyBlocks),
			Placements:          placementsToProtocol(replay.ghosttyPlacements),
			ScrollbackTruncated: replay.scrollbackTruncated,
		}
	}
	d.sendToClient(client, result)
	if !d.life.Go("forwardPTYStreamEvents", func() { d.forwardPTYStreamEvents(client, msg.ID, stream) }) {
		d.detachSession(client, msg.ID)
	}
}

func attachBlocksToProtocol(blocks []pty.AttachBlockData) []protocol.AttachBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]protocol.AttachBlock, len(blocks))
	for i, b := range blocks {
		out[i] = protocol.AttachBlock{
			ID:             int(b.ID),
			Pending:        b.Pending,
			PromptRow:      int(b.PromptRow),
			InputRow:       int32PtrToInt(b.InputRow),
			InputCol:       int32PtrToInt(b.InputCol),
			OutputStartRow: int32PtrToInt(b.OutputStartRow),
			EndRow:         int32PtrToInt(b.EndRow),
			Command:        b.Command,
			ExitCode:       int32PtrToInt(b.ExitCode),
		}
	}
	return out
}

func int32PtrToInt(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func (d *Daemon) handleGetScreenSnapshot(client *wsClient, msg *protocol.GetScreenSnapshotMessage) {
	result, err := Call[protocol.GetScreenSnapshotResultMessage](context.Background(), d.router(), *msg)
	if err != nil {
		result = protocol.GetScreenSnapshotResultMessage{
			Event:   protocol.EventGetScreenSnapshotResult,
			ID:      msg.ID,
			Success: false,
			Error:   protocol.Ptr(err.Error()),
		}
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleDetachSessionWS(client *wsClient, msg *protocol.DetachSessionMessage) {
	d.detachSession(client, msg.ID)
}

func encodePtyOutputMessage(client *wsClient, terminalID protocol.TerminalID, event ptybackend.OutputEvent) (outboundMessage, error) {
	if client.HasCapability(protocol.CapabilityBinaryPtyOutput) {
		frame, err := protocol.EncodePtyOutputFrame(terminalID, event.Seq, event.Data)
		if err != nil {
			return outboundMessage{}, err
		}
		return outboundMessage{kind: messageKindBinary, payload: frame}, nil
	}
	encoded := base64.StdEncoding.EncodeToString(event.Data)
	wsEvent := &protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr(string(terminalID)),
		Data:  protocol.Ptr(encoded),
		Seq:   protocol.Ptr(int(event.Seq)),
	}
	payload, err := json.Marshal(wsEvent)
	if err != nil {
		return outboundMessage{}, err
	}
	return outboundMessage{kind: messageKindText, payload: payload}, nil
}

func (d *Daemon) forwardPTYStreamEvents(client *wsClient, terminalID protocol.TerminalID, stream ptybackend.Stream) {
	d.logf("pty stream forward start: id=%s", terminalID)
	defer func() {
		client.attachMu.Lock()
		current, ok := client.attachedStreams[terminalID]
		if ok && current == stream {
			delete(client.attachedStreams, terminalID)
		}
		client.attachMu.Unlock()
		d.logf("pty stream forward stop: id=%s", terminalID)
	}()

	for event := range stream.Events() {
		acknowledge := func() {
			if acknowledged, ok := stream.(interface{ Acknowledge() }); ok {
				acknowledged.Acknowledge()
			}
		}
		switch event.Kind {
		case ptybackend.OutputEventKindOutput:
			if d.debugLogging {
				d.logf(
					"pty_output forward: id=%s seq=%d bytes=%d preview=%q",
					terminalID,
					event.Seq,
					len(event.Data),
					previewBinaryForLog(event.Data),
				)
			}
			outbound, err := encodePtyOutputMessage(client, terminalID, event)
			if err != nil {
				d.logf("pty_output marshal failed: id=%s seq=%d err=%v", terminalID, event.Seq, err)
				acknowledge()
				continue
			}
			if !d.sendStream(client, outbound) {
				d.logf("pty_output send failed, closing stream: id=%s seq=%d", terminalID, event.Seq)
				d.desyncPTYStream(client, terminalID, stream)
				return
			}
		case ptybackend.OutputEventKindPlacements:
			if !client.HasCapability(protocol.CapabilityKittyImages) {
				acknowledge()
				continue
			}
			outbound, err := encodeKittyPlacementsMessage(terminalID, event)
			if err != nil {
				d.logf("kitty_placements marshal failed: id=%s seq=%d err=%v", terminalID, event.Seq, err)
				acknowledge()
				continue
			}
			if !d.sendStream(client, outbound) {
				d.logf("kitty_placements send failed, closing stream: id=%s seq=%d", terminalID, event.Seq)
				d.desyncPTYStream(client, terminalID, stream)
				return
			}
		case ptybackend.OutputEventKindResize:
			wsEvent := &protocol.WebSocketEvent{
				Event: protocol.EventPtyResized,
				ID:    protocol.Ptr(string(terminalID)),
				Cols:  protocol.Ptr(int(event.Cols)),
				Rows:  protocol.Ptr(int(event.Rows)),
			}
			if event.XPixel > 0 && event.YPixel > 0 {
				wsEvent.Xpixel = protocol.Ptr(int(event.XPixel))
				wsEvent.Ypixel = protocol.Ptr(int(event.YPixel))
			}
			payload, err := json.Marshal(wsEvent)
			if err != nil {
				acknowledge()
				continue
			}
			if !d.sendStream(client, outboundMessage{kind: messageKindText, payload: payload}) {
				d.desyncPTYStream(client, terminalID, stream)
				return
			}
		case ptybackend.OutputEventKindDesync:
			if d.debugLogging {
				d.logf("pty_desync forward: id=%s reason=%s", terminalID, event.Reason)
			}
			wsEvent := &protocol.WebSocketEvent{
				Event:  protocol.EventPtyDesync,
				ID:     protocol.Ptr(string(terminalID)),
				Reason: protocol.Ptr(event.Reason),
			}
			payload, err := json.Marshal(wsEvent)
			if err != nil {
				acknowledge()
				continue
			}
			if !d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: payload}) {
				_ = stream.Close()
				return
			}
			if event.Kind == ptybackend.OutputEventKindDesync {
				acknowledge()
				_ = stream.Close()
				return
			}
		}
		acknowledge()
	}

	d.logf("pty stream events closed: id=%s", terminalID)
	d.resyncEndedStream(client, terminalID, stream)
}

func (d *Daemon) resyncEndedStream(client *wsClient, terminalID protocol.TerminalID, stream ptybackend.Stream) {
	client.attachMu.Lock()
	current, attached := client.attachedStreams[terminalID]
	client.attachMu.Unlock()
	if !attached || current != stream {
		return
	}
	terminal := harness.TerminalID(terminalID)
	if !d.terminalNeedsResync(d.life.Context(), terminal) {
		return
	}
	d.logf("pty stream ended while its terminal runs: id=%s; asking the app to reattach", terminalID)
	d.sendPTYDesync(client, terminalID, "runtime_stream_ended")
}

func (d *Daemon) desyncPTYStream(client *wsClient, terminalID protocol.TerminalID, stream ptybackend.Stream) {
	_ = stream.Close()
	d.sendPTYDesync(client, terminalID, "stream_backpressure")
}

func (d *Daemon) sendPTYDesync(client *wsClient, terminalID protocol.TerminalID, reason string) {
	payload, err := json.Marshal(&protocol.WebSocketEvent{
		Event:  protocol.EventPtyDesync,
		ID:     protocol.Ptr(string(terminalID)),
		Reason: protocol.Ptr(reason),
	})
	if err == nil {
		d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: payload})
	}
}

func (d *Daemon) handlePtyInput(client *wsClient, msg *protocol.PtyInputMessage) {
	d.handlePtyInputs(client, []*protocol.PtyInputMessage{msg})
}

func (d *Daemon) queueRemoteInput(client *wsClient, msg *protocol.PtyInputMessage) {
	if client.terminalOrder.queueInput(msg, func(inputs []*protocol.PtyInputMessage) { d.handlePtyInputs(client, inputs) }) {
		d.runTerminalOrder(client, msg.ID)
	}
}

func (d *Daemon) handlePtyInputs(client *wsClient, inputs []*protocol.PtyInputMessage) {
	receivedAt := time.Now()
	first := inputs[0]
	source := strings.TrimSpace(protocol.Deref(first.Source))
	data := first.Data
	if len(inputs) > 1 {
		var joined strings.Builder
		for _, msg := range inputs {
			joined.WriteString(msg.Data)
		}
		data = joined.String()
	}
	if d.debugLogging {
		d.logf("pty_input: id=%s bytes=%d preview=%q source=%s", first.ID, len(data), previewBinaryForLog([]byte(data)), source)
	}
	_, writeErr := Call[struct{}](context.Background(), d.router(), protocol.TerminalWriteMessage{Cmd: protocol.CmdTerminalWrite, TerminalID: first.ID, Data: data, Source: source})
	elapsed := time.Since(receivedAt)
	for _, msg := range inputs {
		d.recordSupportInputTrace(msg, receivedAt, elapsed, writeErr)
		if writeErr != nil {
			if d.sessionHost(d.terminals().owner(msg.ID)) != "" {
				d.sendCommandError(client, protocol.CmdPtyInput, writeErr.Error())
			}
			if shouldLogPtyCommandError(writeErr) {
				d.logf("pty_input failed for %s: %v", msg.ID, writeErr)
			}
		} else if d.debugLogging {
			d.logf("pty_input ok: id=%s bytes=%d", msg.ID, len(msg.Data))
		}
		if probeID := strings.TrimSpace(protocol.Deref(msg.ProbeID)); probeID != "" && client != nil {
			result := &protocol.PtyInputProbeResultMessage{Event: protocol.EventPtyInputProbeResult, ID: msg.ID, ProbeID: probeID, Success: writeErr == nil, WriteDurationUs: int(elapsed.Microseconds())}
			if writeErr != nil {
				result.Error = protocol.Ptr(writeErr.Error())
			}
			d.sendToClient(client, result)
		}
	}
}

func (d *Daemon) handleTerminalPointerActivity(msg *protocol.TerminalPointerActivityMessage) {
	_, _ = Call[struct{}](context.Background(), d.router(), *msg)
}

type ptyGeometry struct {
	Cols   int `json:"cols"`
	Rows   int `json:"rows"`
	XPixel int `json:"xpixel,omitempty"`
	YPixel int `json:"ypixel,omitempty"`
}

func (d *Daemon) projectSessionPTYResized(ev bus.Event) {
	geometry, ok := decodeFact[ptyGeometry](d, ev)
	if !ok {
		return
	}
	event := &protocol.WebSocketEvent{
		Event: protocol.EventPtyResized,
		ID:    protocol.Ptr(ev.Subject),
		Cols:  protocol.Ptr(geometry.Cols),
		Rows:  protocol.Ptr(geometry.Rows),
	}
	if geometry.XPixel > 0 && geometry.YPixel > 0 {
		event.Xpixel = protocol.Ptr(geometry.XPixel)
		event.Ypixel = protocol.Ptr(geometry.YPixel)
	}
	d.wsHub.Broadcast(event)
}

func (d *Daemon) handlePtyResize(client *wsClient, msg *protocol.PtyResizeMessage) {
	if msg.Cols <= 0 || msg.Rows <= 0 || msg.Cols > maxPTYDimValue || msg.Rows > maxPTYDimValue {
		d.sendCommandError(client, protocol.CmdPtyResize, fmt.Sprintf("invalid terminal size cols=%d rows=%d (expected 1..%d)", msg.Cols, msg.Rows, maxPTYDimValue))
		return
	}
	if _, err := Call[struct{}](context.Background(), d.router(), *msg); err != nil && shouldLogPtyCommandError(err) {
		d.logf("pty_resize failed for %s: %v", msg.ID, err)
		if d.sessionHost(d.terminals().owner(msg.ID)) != "" {
			d.sendCommandError(client, protocol.CmdPtyResize, err.Error())
		}
	}
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func sanitizeThemeColor(value string) string {
	if hexColorPattern.MatchString(value) {
		return value
	}
	return ""
}

func sanitizeANSIPalette(values []string) (palette [16]string, ok bool) {
	if len(values) != len(palette) {
		return palette, false
	}
	for i, value := range values {
		if !hexColorPattern.MatchString(value) {
			return [16]string{}, false
		}
		palette[i] = value
	}
	return palette, true
}

func (d *Daemon) handleSetTerminalTheme(client *wsClient, msg *protocol.SetTerminalThemeMessage) {
	ansiPalette, paletteOK := sanitizeANSIPalette(msg.AnsiPalette)
	theme := pty.TerminalTheme{
		Foreground:  sanitizeThemeColor(msg.Foreground),
		Background:  sanitizeThemeColor(msg.Background),
		Cursor:      sanitizeThemeColor(msg.Cursor),
		ANSIPalette: ansiPalette,
	}
	if theme.Foreground != msg.Foreground || theme.Background != msg.Background || theme.Cursor != msg.Cursor || !paletteOK {
		d.logf("set_terminal_theme: invalid color field(s) blanked, got fg=%q bg=%q cursor=%q palette_len=%d", msg.Foreground, msg.Background, msg.Cursor, len(msg.AnsiPalette))
	}
	_, _ = Call[struct{}](context.Background(), d.router(), protocol.TerminalThemeMessage{
		Cmd: protocol.CmdTerminalTheme, Foreground: theme.Foreground, Background: theme.Background, Cursor: theme.Cursor, AnsiPalette: theme.ANSIPalette[:],
	})
	for _, outpost := range d.outpostsUpdatedMessage().Outposts {
		if outpost.State == protocol.OutpostStateConnected || outpost.State == protocol.OutpostStateStalled {
			d.life.Go("outpostTheme", func() {
				if err := d.sendOutpostTheme(d.life.Context(), outpost.ID); err != nil {
					d.logf("outpost theme: %v", err)
				}
			})
		}
	}
}

func parseSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	case "SIGHUP", "HUP":
		return syscall.SIGHUP
	case "SIGKILL", "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func (d *Daemon) handleKillSession(client *wsClient, msg *protocol.KillSessionMessage) {
	sessionID := msg.ID
	if err := d.requireSessionHost(sessionID); err != nil {
		d.sendCommandError(client, protocol.CmdKillSession, err.Error())
		return
	}
	terminals := d.terminalsOf(sessionID)
	for _, terminal := range terminals {
		d.detachSession(client, terminal)
	}
	sig := parseSignal(protocol.Deref(msg.Signal))
	if d.sessionHost(sessionID) != "" {
		d.killSessionRuntimeAsync(sessionID, terminals, sig)
	} else {
		d.life.Go("killSessionRuntimeAsync", func() { d.killSessionRuntimeAsync(sessionID, terminals, sig) })
	}
}

func (d *Daemon) killSessionRuntimeAsync(sessionID protocol.SessionID, terminals []harness.TerminalID, sig syscall.Signal) {
	var err error
	for _, terminal := range terminals {
		if _, killErr := Call[struct{}](context.Background(), d.router(), protocol.TerminalKillMessage{Cmd: protocol.CmdTerminalKill, TerminalID: terminal, Signal: signalName(sig), Stop: protocol.Ptr(true)}); killErr != nil {
			if err == nil || errors.Is(err, pty.ErrSessionNotFound) {
				err = killErr
			}
		}
	}
	if err == nil || errors.Is(err, pty.ErrSessionNotFound) {
		d.closePluginDriverSession(sessionID, "killed", nil, signalName(sig))
	}
	if err != nil {
		if shouldLogPtyCommandError(err) {
			d.logf("kill_session failed for %s: %v", sessionID, err)
		}
	}
}

func shouldLogPtyCommandError(err error) bool {
	return !errors.Is(err, pty.ErrSessionNotFound)
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return "SIGTERM"
	}
}
