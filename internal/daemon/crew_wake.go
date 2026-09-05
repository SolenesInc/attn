package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	agentdriver "github.com/victorarias/attn/internal/agent"
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

func (d *Daemon) crewAgentAvailable(agent string) bool {
	if agentdriver.Get(agent) != nil {
		return true
	}
	_, ok := d.ensurePluginRegistry().driver(agent)
	return ok
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
	if d.isHostSession(sessionID) {
		return true, nil
	}
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
	member, doc, err := d.crewMember(strings.TrimSpace(msg.Member))
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	if msg.Cwd != nil {
		cwd, err := d.resolveCrewRecordedDir(*msg.Cwd)
		if err != nil {
			d.sendCrewError(conn, "set", err)
			return
		}
		member.CWD = cwd
	}
	if msg.Agent != nil {
		agent := strings.TrimSpace(strings.ToLower(*msg.Agent))
		if agent != "" && !d.crewAgentAvailable(agent) {
			d.sendCrewError(conn, "set", fmt.Errorf("agent %q is not available; `attn agent list` names the harnesses this daemon can launch", agent))
			return
		}
		member.Agent = agent
	}
	if msg.Model != nil {
		member.Model = strings.TrimSpace(*msg.Model)
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
				d.sendCrewError(conn, "set", err)
				return
			}
			if resolved != "" {
				dirs = append(dirs, resolved)
			}
		}
		member.AwarenessDirs = dirs
	}
	if err := d.writeCrewMember(*schema, member, doc.Rev); err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.sendGardenResponse(conn, protocol.Response{
		Ok:            true,
		CrewSetResult: &protocol.CrewSetResult{Member: d.crewMemberWire(member)},
	})
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
