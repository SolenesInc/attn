package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const crewWakeAgent = crew.DefaultAgent

const crewWakeFallbackModel = "fable"

func (d *Daemon) crewWakeModel(member crew.Member, agent string) *string {
	// A one-day harness override must not receive a model chosen for the
	// member's usual harness (for example, a Claude id passed to Codex).
	if strings.EqualFold(strings.TrimSpace(agent), member.LaunchAgent()) {
		if model := strings.TrimSpace(member.Model); model != "" {
			return protocol.Ptr(model)
		}
	}
	if model := d.defaultLaunchModel(agent); model != "" {
		return protocol.Ptr(model)
	}
	if strings.TrimSpace(strings.ToLower(agent)) == crewWakeAgent {
		return protocol.Ptr(crewWakeFallbackModel)
	}
	return nil
}

func (d *Daemon) crewWakeEffort(member crew.Member, agent string) *string {
	if strings.EqualFold(strings.TrimSpace(agent), member.LaunchAgent()) {
		if effort := strings.TrimSpace(member.Effort); effort != "" {
			return protocol.Ptr(effort)
		}
	}
	if effort := d.defaultLaunchEffort(agent); effort != "" {
		return protocol.Ptr(effort)
	}
	return nil
}

func (d *Daemon) crewAgentAvailable(agent string) bool {
	agent = strings.TrimSpace(strings.ToLower(agent))
	for _, harness := range d.delegationHarnesses() {
		if harness.ID == agent {
			return harness.Available
		}
	}
	return false
}

var crewWakePrompt = prompts.RenderText("crew", "wake", prompts.Values{})

func crewWorkspaceID(memberID string) string { return "workspace-crew-" + memberID }

type crewWakeDelivery struct {
	Message *agentmailbox.PeerMessage
}

func (d *Daemon) crewMember(name string) (crew.Member, docstore.Document, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	member, ok := crew.Resolve(name, members)
	if !ok {
		return crew.Member{}, docstore.Document{}, fmt.Errorf("no crew member %q is registered; `attn crew list` names the roster", name)
	}
	return member, docs[member.ID], nil
}

func (d *Daemon) crewLaunchDir(member crew.Member) (string, error) {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return "", err
	}
	if err := d.validateCrewAwarenessDirs(member); err != nil {
		return "", err
	}
	dir := strings.TrimSpace(member.CWD)
	if dir == "" {
		return member.HomeDir, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("%s launches in %s, which is not there (%v); `attn crew set %s --cwd <dir>` moves it", crew.DisplayName(member.ID), dir, err, member.ID)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s launches in %s, which is not a directory; `attn crew set %s --cwd <dir>` moves it", crew.DisplayName(member.ID), dir, member.ID)
	}
	resolved, err := d.resolveCrewWorkDir(dir)
	if err != nil {
		return "", fmt.Errorf("%s launches in %s: %w", crew.DisplayName(member.ID), dir, err)
	}
	return resolved, nil
}

func (d *Daemon) crewPriming(member crew.Member) (crew.Priming, error) {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return crew.Priming{}, err
	}
	if err := d.validateCrewWorkDirs(member); err != nil {
		return crew.Priming{}, err
	}
	priming := crew.Priming{
		Member:        member.ID,
		HomeDir:       member.HomeDir,
		CharterPath:   member.CharterPath,
		CWD:           member.CWD,
		AwarenessDirs: member.AwarenessDirs,
	}
	if charter, err := os.ReadFile(member.CharterPath); err == nil {
		priming.Charter = string(charter)
	} else if !os.IsNotExist(err) {
		d.logf("crew: reading %s's charter at %s: %v", crew.DisplayName(member.ID), member.CharterPath, err)
	}

	handoffsDir, err := d.validateCrewHandoffsDir(member)
	if err != nil {
		return crew.Priming{}, err
	}
	entries, err := os.ReadDir(handoffsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			d.logf("crew: reading %s's handoffs at %s: %v", crew.DisplayName(member.ID), handoffsDir, err)
		}
		return priming, nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	if len(names) == 0 {
		return priming, nil
	}
	for _, name := range names {
		if err := d.validateCrewLetterPath(member, filepath.Join(handoffsDir, name)); err != nil {
			return crew.Priming{}, err
		}
	}
	priming.HandoffName = names[0]
	priming.OlderHandoffs = names[1:]
	letterPath := filepath.Join(handoffsDir, names[0])
	if letter, err := os.ReadFile(letterPath); err == nil {
		priming.Handoff = string(letter)
	} else {
		d.logf("crew: reading %s's freshest handoff %s: %v", crew.DisplayName(member.ID), names[0], err)
	}
	return priming, nil
}

func (d *Daemon) handleCrewWake(conn net.Conn, msg *protocol.CrewWakeMessage) {
	result, err := d.crewWake(strings.TrimSpace(msg.Member), strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Agent))))
	if err != nil {
		d.sendCrewError(conn, "wake", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewWakeResult: result})
}

func (d *Daemon) handleCrewWakeWS(client *wsClient, msg *protocol.CrewWakeMessage) {
	result, err := d.crewWake(strings.TrimSpace(msg.Member), strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Agent))))
	response := protocol.CrewWakeResultMessage{
		Event:     protocol.EventCrewWakeResult,
		RequestID: protocol.Deref(msg.RequestID),
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member = protocol.Ptr(result.Member)
		response.SessionID = protocol.Ptr(result.SessionID)
		response.WorkspaceID = protocol.Ptr(result.WorkspaceID)
		if result.AlreadyAwake {
			response.AlreadyAwake = protocol.Ptr(true)
		}
		response.ReleasedSessionID = result.ReleasedSessionID
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewWake(name, agent string) (*protocol.CrewWakeResult, error) {
	return d.crewWakeWithDelivery(name, agent, false, nil)
}

func (d *Daemon) crewWakeWithDelivery(name, agent string, autonomous bool, delivery *crewWakeDelivery) (*protocol.CrewWakeResult, error) {
	if d.crewWakeStartHook != nil {
		d.crewWakeStartHook(strings.TrimSpace(strings.ToLower(name)))
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	return d.crewWakeWithDeliveryLocked(name, agent, autonomous, delivery)
}

func (d *Daemon) crewWakeWithDeliveryLocked(name, agent string, autonomous bool, delivery *crewWakeDelivery) (*protocol.CrewWakeResult, error) {
	member, _, err := d.crewMember(name)
	if err != nil {
		return nil, err
	}
	releasedSessionID := d.takeCrewExitedSession(member.ID)
	if boundSessionID := strings.TrimSpace(member.BindingSession); boundSessionID != "" {
		live, err := d.crewSessionActuallyLive(boundSessionID)
		if err != nil {
			return nil, fmt.Errorf("check %s's bound session %s: %w", crew.DisplayName(member.ID), shortSessionID(boundSessionID), err)
		}
		if !live {
			if _, err := d.releaseCrewBinding(member.ID, boundSessionID); err != nil {
				return nil, fmt.Errorf("release %s's exited session %s: %w", crew.DisplayName(member.ID), shortSessionID(boundSessionID), err)
			}
			releasedSessionID = boundSessionID
		} else {
			awake := &protocol.CrewWakeResult{
				Member:       member.ID,
				SessionID:    boundSessionID,
				AlreadyAwake: true,
			}
			if session := d.store.Get(boundSessionID); session != nil {
				awake.WorkspaceID = session.WorkspaceID
			}
			return awake, nil
		}
	}
	if agent == "" {
		agent = member.LaunchAgent()
	}
	if !d.crewAgentAvailable(agent) {
		return nil, fmt.Errorf("agent %q is not available", agent)
	}
	directory, err := d.crewLaunchDir(member)
	if err != nil {
		return nil, err
	}
	if autonomous {
		if err := d.chargeAutonomousWake(member.ID, time.Now()); err != nil {
			return nil, fmt.Errorf("%w; nothing was delivered", err)
		}
	}

	sessionID := uuid.NewString()
	// The launch reads the binding through `crew_prime`, so it must be claimed
	// before the spawn; a failed launch releases it below.
	if _, err := d.claimCrewBinding(member.ID, sessionID); err != nil {
		return nil, err
	}
	if d.crewWakeAfterClaimHook != nil {
		d.crewWakeAfterClaimHook(member.ID, sessionID)
	}

	workspaceID := crewWorkspaceID(member.ID)
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     crew.DisplayName(member.ID),
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			d.releaseCrewBindingIfSession(sessionID)
			return nil, fmt.Errorf("create %s's workspace", crew.DisplayName(member.ID))
		}
	}
	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-" + sessionID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(crew.DisplayName(member.ID)),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		d.releaseCrewBindingIfSession(sessionID)
		return nil, fmt.Errorf("create %s's pane: %w", crew.DisplayName(member.ID), err)
	}

	initialPrompt := crewWakePrompt
	// A crew binding becomes visible before the launching agent has crossed priming and its
	// trust dialog. This marker protects unrelated foreground input, not mailbox delivery.
	d.notePostInitialPrompt(sessionID)
	if delivery != nil {
		if delivery.Message != nil {
			if _, err := d.store.EnqueuePeerMessage(*delivery.Message, sessionID); err != nil {
				d.removeWorkspaceLayoutPaneForSession(sessionID)
				d.releaseCrewBindingIfSession(sessionID)
				return nil, err
			}
			d.noteQueuedAgentMailboxItem(sessionID)
		}
	}

	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		WorkspaceID:   workspaceID,
		Agent:         agent,
		Model:         d.crewWakeModel(member, agent),
		Effort:        d.crewWakeEffort(member, agent),
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(crew.DisplayName(member.ID)),
		InitialPrompt: protocol.Ptr(initialPrompt),
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		if delivery != nil && delivery.Message != nil {
			d.rollbackQueuedPeerMessage(sessionID, delivery.Message.ID)
		}
		d.forgetPostInitialPrompt(sessionID)
		d.removeWorkspaceLayoutPaneForSession(sessionID)
		d.releaseCrewBindingIfSession(sessionID)
		return nil, fmt.Errorf("wake %s: %w", crew.DisplayName(member.ID), err)
	}
	d.logf("crew: woke %s in session %s at %s", crew.DisplayName(member.ID), sessionID, directory)
	result := &protocol.CrewWakeResult{
		Member:      member.ID,
		SessionID:   sessionID,
		WorkspaceID: workspaceID,
	}
	if releasedSessionID != "" {
		result.ReleasedSessionID = protocol.Ptr(releasedSessionID)
	}
	return result, nil
}

func (d *Daemon) crewSessionActuallyLive(sessionID string) (bool, error) {
	if d.store == nil || d.store.Get(sessionID) == nil {
		return false, nil
	}
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return true, nil
	}
	info, err := provider.SessionInfo(context.Background(), sessionID)
	if err == nil {
		return info.Running, nil
	}
	if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (d *Daemon) handleCrewPrime(conn net.Conn, msg *protocol.CrewPrimeMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "prime", err)
		return
	}
	result := &protocol.CrewPrimeResult{AwarenessDirs: []string{}}
	member, block, bound, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.sendCrewError(conn, "prime", err)
		return
	}
	if bound {
		result.Member = protocol.Ptr(member.ID)
		result.Guidance = protocol.Ptr(block)
		result.AwarenessDirs = append(result.AwarenessDirs, member.AwarenessDirs...)
		result.PrimingBytes = len(block)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewPrimeResult: result})
}

func (d *Daemon) crewPrimeForSession(sessionID string) (crew.Member, string, bool, error) {
	if sessionID == "" || d.store == nil {
		return crew.Member{}, "", false, nil
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, "", false, err
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to prime %s: %v", sessionID, err)
		}
		return crew.Member{}, "", false, err
	}
	for _, member := range members {
		if member.BindingSession != sessionID {
			continue
		}
		priming, err := d.crewPriming(member)
		if err != nil {
			return crew.Member{}, "", false, err
		}
		block := priming.Block()
		handoff := priming.HandoffName
		if handoff == "" {
			handoff = "(none)"
		}
		d.logf("crew: priming %s for session %s: %d bytes (charter %d, handoff %s %d, older %d)",
			crew.DisplayName(member.ID), sessionID, len(block), len(priming.Charter),
			handoff, len(priming.Handoff), len(priming.OlderHandoffs))
		return member, block, true, nil
	}
	return crew.Member{}, "", false, nil
}

func (d *Daemon) handleCrewSet(conn net.Conn, msg *protocol.CrewSetMessage) {
	member, conflict, err := d.crewSet(msg)
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	if conflict {
		d.sendCrewError(conn, "set", errors.New("the member changed after it was read; reconcile the returned revision and retry"))
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewSetResult: &protocol.CrewSetResult{Member: *member}})
}

func (d *Daemon) handleCrewSetWS(client *wsClient, msg *protocol.CrewSetMessage) {
	if strings.TrimSpace(protocol.Deref(msg.RequestID)) == "" {
		d.sendToClient(client, protocol.CrewSetResultMessage{
			Event: protocol.EventCrewSetResult, Success: false, Conflict: false,
			Error: protocol.Ptr("missing request id"),
		})
		return
	}
	member, conflict, err := d.crewSet(msg)
	result := protocol.CrewSetResultMessage{
		Event: protocol.EventCrewSetResult, RequestID: protocol.Deref(msg.RequestID),
		Success: err == nil && !conflict, Conflict: conflict, Member: member,
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else if conflict {
		result.Error = protocol.Ptr("the member changed after it was read; reconcile the returned revision and retry")
	}
	d.sendToClient(client, result)
}

// crewSet is shared by IPC and WS. expected_revision opts into strict CAS;
// omission reapplies the patch to the latest record after an ordinary race.
func (d *Daemon) crewSet(msg *protocol.CrewSetMessage) (*protocol.CrewMember, bool, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return nil, false, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return nil, false, err
	}
	for {
		member, doc, err := d.crewMember(strings.TrimSpace(msg.Member))
		if err != nil {
			return nil, false, err
		}
		if msg.ExpectedRevision != nil && int64(*msg.ExpectedRevision) != doc.Rev {
			wire := d.crewMemberWire(member, doc.Rev)
			return &wire, true, nil
		}
		if err := d.applyCrewSettings(&member, msg); err != nil {
			return nil, false, err
		}
		revision, err := d.writeCrewMember(*schema, member, doc.Rev)
		if err == nil {
			d.publishFact(FactCrewUpdated, member.ID, nil)
			wire := d.crewMemberWire(member, revision)
			return &wire, false, nil
		}
		if !docstore.IsConflict(err) {
			return nil, false, err
		}
		if msg.ExpectedRevision != nil {
			current, currentDoc, readErr := d.crewMember(strings.TrimSpace(msg.Member))
			if readErr != nil {
				return nil, false, readErr
			}
			wire := d.crewMemberWire(current, currentDoc.Rev)
			return &wire, true, nil
		}
	}
}

func (d *Daemon) applyCrewSettings(member *crew.Member, msg *protocol.CrewSetMessage) error {
	if msg.Cwd != nil {
		cwd, err := d.resolveCrewRecordedDir(*msg.Cwd)
		if err != nil {
			return err
		}
		member.CWD = cwd
	}
	agentChanged := false
	if msg.Agent != nil {
		before := member.LaunchAgent()
		member.Agent = strings.TrimSpace(strings.ToLower(*msg.Agent))
		agentChanged = !strings.EqualFold(before, member.LaunchAgent())
		if agentChanged && msg.Model == nil {
			member.Model = ""
		}
		if agentChanged && msg.Effort == nil {
			member.Effort = ""
		}
	}
	if msg.Model != nil {
		member.Model = strings.TrimSpace(*msg.Model)
	}
	if msg.Effort != nil {
		member.Effort = strings.TrimSpace(strings.ToLower(*msg.Effort))
	}
	if msg.Agent != nil || msg.Model != nil || msg.Effort != nil {
		requireAvailable := (msg.Agent != nil && strings.TrimSpace(*msg.Agent) != "") ||
			(msg.Model != nil && strings.TrimSpace(*msg.Model) != "") ||
			(msg.Effort != nil && strings.TrimSpace(*msg.Effort) != "")
		if err := d.validateCrewLaunchSelection(*member, requireAvailable); err != nil {
			return err
		}
	}
	// The way out arrives as its own flag: an empty list marshals away, so an
	// empty AwarenessDirs is indistinguishable from "leave it alone" on the wire.
	if protocol.Deref(msg.ClearAwarenessDirs) {
		member.AwarenessDirs = nil
	} else if msg.AwarenessDirs != nil {
		dirs := make([]string, 0, len(msg.AwarenessDirs))
		for _, dir := range msg.AwarenessDirs {
			resolved, err := d.resolveCrewRecordedDir(dir)
			if err != nil {
				return err
			}
			if resolved != "" {
				dirs = append(dirs, resolved)
			}
		}
		member.AwarenessDirs = dirs
	}
	return nil
}

func (d *Daemon) validateCrewLaunchSelection(member crew.Member, requireAvailable bool) error {
	agent := member.LaunchAgent()
	var harness *protocol.DelegationHarness
	for _, candidate := range d.delegationHarnesses() {
		if candidate.ID == agent {
			copy := candidate
			harness = &copy
			break
		}
	}
	if harness == nil {
		return fmt.Errorf("agent %q is not available; the harness catalog names what this daemon can launch", agent)
	}
	if requireAvailable && !harness.Available {
		return fmt.Errorf("agent %q is installed but its executable or driver is unavailable", agent)
	}
	if err := d.validateDelegationModelEffort(agent, member.Model, member.Effort); err != nil {
		return err
	}
	if member.Model == "" || !harness.Discovery || !harness.Available {
		return nil
	}
	catalog, err := d.discoverDelegationModels(context.Background(), agent)
	if err != nil {
		return fmt.Errorf("validate model %q: %w", member.Model, err)
	}
	for _, model := range catalog.Models {
		if model.ID != member.Model {
			continue
		}
		if model.Access == protocol.ModelCapabilitySupportUnsupported {
			return fmt.Errorf("model %q is unavailable: %s", member.Model, model.Detail)
		}
		if member.Effort != "" && (model.EffortSupport == protocol.ModelCapabilitySupportUnsupported || (len(model.EffortLevels) > 0 && !slices.Contains(model.EffortLevels, member.Effort))) {
			return fmt.Errorf("model %q does not support effort %q", member.Model, member.Effort)
		}
		break
	}
	return nil
}

func absoluteCrewDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%s is not a usable path: %w", dir, err)
	}
	return absolute, nil
}

func resolveCrewDir(dir string) (string, error) {
	absolute, err := absoluteCrewDir(dir)
	if err != nil || absolute == "" {
		return absolute, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("%s is not there", absolute)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absolute)
	}
	return absolute, nil
}
