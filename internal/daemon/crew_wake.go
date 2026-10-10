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
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/who"
)

const crewWakeAgent = crew.DefaultAgent

const crewWakeFallbackModel = "fable"

func (d *Daemon) crewWakeModel(member crew.Member, agent string) *string {
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

// A wake the user did not start is announced with who asked for it.
type crewWakeRequest struct {
	RequestedBy string
	UserStarted bool
}

func (d *Daemon) crewMember(key who.MemberKey) (crew.Member, docstore.Document, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	doc, found, err := d.store.GetDocument(*schema, key.String())
	if err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	if !found {
		return crew.Member{}, docstore.Document{}, fmt.Errorf("crew member %s not found", key)
	}
	member, err := crew.Decode(doc.ID, doc.Body)
	if err == nil {
		err = d.validateCrewMemberPaths(member)
	}
	return member, *doc, err
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
		return "", fmt.Errorf("%s launches in %s, which is not there (%v); `attn crew set %s --cwd <dir>` moves it", d.storedMemberName(member.Key.String()), dir, err, member.Key.String())
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s launches in %s, which is not a directory; `attn crew set %s --cwd <dir>` moves it", d.storedMemberName(member.Key.String()), dir, member.Key.String())
	}
	resolved, err := d.resolveCrewWorkDir(dir)
	if err != nil {
		return "", fmt.Errorf("%s launches in %s: %w", d.storedMemberName(member.Key.String()), dir, err)
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
		Name:          d.memberName(member.Key),
		HomeDir:       member.HomeDir,
		CharterPath:   member.CharterPath,
		CWD:           member.CWD,
		AwarenessDirs: member.AwarenessDirs,
	}
	d.primeCrewGarden(&priming, member.Key.String())
	if charter, err := os.ReadFile(member.CharterPath); err == nil {
		priming.Charter = string(charter)
	} else if !os.IsNotExist(err) {
		d.logf("crew: reading %s's charter at %s: %v", member.Key.String(), member.CharterPath, err)
	}

	handoffsDir, err := d.validateCrewHandoffsDir(member)
	if err != nil {
		return crew.Priming{}, err
	}
	entries, err := os.ReadDir(handoffsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			d.logf("crew: reading %s's handoffs at %s: %v", member.Key.String(), handoffsDir, err)
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
		d.logf("crew: reading %s's freshest handoff %s: %v", member.Key.String(), names[0], err)
	}
	return priming, nil
}

func (d *Daemon) primeCrewGarden(priming *crew.Priming, memberID string) {
	read, err := d.readGardenTo(0, d.crewProfileID(memberID))
	if err != nil {
		d.logf("crew: reading the garden to prime %s: %v", memberID, err)
		return
	}
	priming.GardenRead = true
	held := garden.Held(read.seeds, memberID)
	priming.HeldTotal = len(held)
	if len(held) > crew.MaxHeldSeeds {
		held = held[:crew.MaxHeldSeeds]
	}
	for _, seed := range held {
		entry := crew.HeldSeed{ID: seed.ID, Slug: seed.StepSlug, Title: seed.Title}
		if handoff := d.gardenHandoff(seed.ID); handoff != nil {
			entry.Handoff = handoff.Body
		}
		priming.Held = append(priming.Held, entry)
	}
	for _, plot := range garden.PlotsOf(read.seeds, held) {
		priming.Plots = append(priming.Plots, crew.PlotReady{
			ID:    plot.ID,
			Slug:  plot.StepSlug,
			Title: plot.Title,
			Ready: garden.PlotProgress(read.seeds, plot.ID, read.ready).Ready,
		})
	}
}

func (d *Daemon) crewWakeAsked(msg *protocol.CrewWakeMessage) (*protocol.CrewWakeResult, error) {
	return d.crewWakeAskedFor(msg, false)
}

func (d *Daemon) crewWakeAskedFor(msg *protocol.CrewWakeMessage, userStarted bool) (*protocol.CrewWakeResult, error) {
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		return nil, bindingsErr
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if err != nil {
		return nil, err
	}
	identity, err := d.resolveMember(r, msg.Member)
	if err != nil {
		return nil, err
	}
	key := identity.Key
	request := crewWakeRequest{UserStarted: userStarted, RequestedBy: d.launchRequester(protocol.Deref(msg.SourceSessionID), "attn crew wake")}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	return d.crewWakeDayWithChargeLocked(key, strings.TrimSpace(strings.ToLower(protocol.Deref(msg.Agent))), false, nil, request)
}

func (d *Daemon) handleCrewWake(conn net.Conn, msg *protocol.CrewWakeMessage) {
	result, err := d.crewWakeAsked(msg)
	if err != nil {
		d.sendCrewError(conn, "wake", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewWakeResult: result})
}

func (d *Daemon) handleCrewWakeWS(client *wsClient, msg *protocol.CrewWakeMessage) {
	scoped := *msg
	scoped.SourceSessionID = nil
	scoped.ProfileID = protocol.Ptr(client.selectedProfile())
	result, err := d.crewWakeAskedFor(&scoped, true)
	var showErr error
	if err == nil {
		showErr = d.showCrewWake(result, client, protocol.Deref(msg.RequestID))
	}
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
		response.ProfileID = protocol.Ptr(result.ProfileID)
		if result.AlreadyAwake {
			response.AlreadyAwake = protocol.Ptr(true)
		}
		response.ReleasedSessionID = result.ReleasedSessionID
		if showErr != nil {
			response.ShowError = protocol.Ptr(showErr.Error())
		}
	}
	d.sendToClient(client, response)
}

func (d *Daemon) crewWakeWithChargeLocked(key who.MemberKey, agent string, autonomous bool) (*protocol.CrewWakeResult, error) {
	return d.crewWakeDayWithChargeLocked(key, agent, autonomous, nil, crewWakeRequest{})
}
func (d *Daemon) crewWakeDayWithChargeLocked(key who.MemberKey, agent string, autonomous bool, beforeWake func() error, request crewWakeRequest) (*protocol.CrewWakeResult, error) {
	member, _, err := d.crewMember(key)
	if err != nil {
		return nil, err
	}
	releasedSessionID := protocol.SessionID(d.takeCrewExitedSession(member.Key.String()))
	if boundSessionID := protocol.TrimID(member.BindingSession); boundSessionID != "" {
		live, err := d.crewSessionActuallyLive(boundSessionID)
		if err != nil {
			return nil, fmt.Errorf("check %s's bound session %s: %w", d.storedMemberName(member.Key.String()), shortSessionID(boundSessionID), err)
		}
		if !live {
			if _, err := d.releaseCrewBinding(member.Key, boundSessionID); err != nil {
				return nil, fmt.Errorf("release %s's exited session %s: %w", d.storedMemberName(member.Key.String()), shortSessionID(boundSessionID), err)
			}
			releasedSessionID = boundSessionID
		} else {
			awake := &protocol.CrewWakeResult{
				Member:       member.Key.String(),
				Name:         d.memberName(member.Key),
				SessionID:    boundSessionID,
				AlreadyAwake: true,
			}
			if session := d.store.Get(boundSessionID); session != nil {
				awake.ProfileID = session.ProfileID
			}
			return awake, nil
		}
	}
	if beforeWake != nil {
		if err := beforeWake(); err != nil {
			return nil, err
		}
	}
	profile, err := d.liveLaunchProfile(d.crewProfileID(member.Key.String()))
	if err != nil {
		return nil, fmt.Errorf("wake %s: %w", d.storedMemberName(member.Key.String()), err)
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
		if err := d.chargeAutonomousWake(member.Key, time.Now()); err != nil {
			return nil, fmt.Errorf("%w; nothing was delivered", err)
		}
	}

	sessionID := protocol.SessionID(uuid.NewString())
	if _, err := d.claimCrewBinding(member.Key, sessionID); err != nil {
		return nil, err
	}

	initialPrompt := crewWakePrompt
	spawnClient := newInternalWSClient()
	d.handleSpawnSessionWithPolicy(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:           protocol.CmdSpawnSession,
		ID:            sessionID,
		Cwd:           directory,
		ProfileID:     profile.ID,
		Agent:         agent,
		Model:         d.crewWakeModel(member, agent),
		Effort:        d.crewWakeEffort(member, agent),
		Cols:          80,
		Rows:          24,
		Label:         protocol.Ptr(d.storedMemberName(member.Key.String())),
		InitialPrompt: protocol.Ptr(initialPrompt),
	}, internalSpawnPolicy{member: member.Key, launchPlacement: &launchPlacement{kind: "crew", itemID: member.Key.String()}})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		d.releaseCrewBindingIfSession(sessionID)
		return nil, fmt.Errorf("wake %s: %w", d.storedMemberName(member.Key.String()), err)
	}
	if !request.UserStarted {
		requester := request.RequestedBy
		if requester == "" {
			requester = "a garden notification"
		}
		d.announceBackgroundLaunch("crew", member.Key.String(), sessionID, requester)
	}
	d.logf("crew: woke %s in session %s at %s", member.Key.String(), sessionID, directory)
	result := &protocol.CrewWakeResult{
		Member:    member.Key.String(),
		Name:      d.memberName(member.Key),
		SessionID: sessionID,
		ProfileID: profile.ID,
	}
	if releasedSessionID != "" {
		result.ReleasedSessionID = protocol.Ptr(releasedSessionID)
	}
	return result, nil
}

func (d *Daemon) crewSessionActuallyLive(sessionID protocol.SessionID) (bool, error) {
	if d.store == nil || d.store.Get(sessionID) == nil {
		return false, nil
	}
	provider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return true, nil
	}
	info, err := provider.SessionInfo(context.Background(), d.primaryTerminal(sessionID))
	if err == nil {
		return info.Running, nil
	}
	if errors.Is(err, pty.ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (d *Daemon) handleCrewPrime(conn net.Conn, msg *protocol.CrewPrimeMessage) {
	sessionID := protocol.TrimID(msg.SessionID)
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
		result.Member = protocol.Ptr(member.Key.String())
		result.Guidance = protocol.Ptr(block)
		result.AwarenessDirs = append(result.AwarenessDirs, member.AwarenessDirs...)
		result.PrimingBytes = len(block)
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewPrimeResult: result})
}

func (d *Daemon) crewPrimeForSession(sessionID protocol.SessionID) (crew.Member, string, bool, error) {
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
		d.logf("crew: priming %s for session %s: %d bytes (charter %d, handoff %s %d, older %d, garden %d for %d of %d held and %d plots)",
			d.storedMemberName(member.Key.String()), sessionID, len(block), len(priming.Charter),
			handoff, len(priming.Handoff), len(priming.OlderHandoffs),
			len(priming.GardenSection()), len(priming.Held), priming.HeldTotal, len(priming.Plots))
		return member, block, true, nil
	}
	return crew.Member{}, "", false, nil
}

func (d *Daemon) handleCrewSet(conn net.Conn, msg *protocol.CrewSetMessage) {
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		d.sendError(conn, bindingsErr.Error())
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if err != nil {
		d.sendCrewError(conn, "set", err)
		return
	}
	identity, scopeErr := d.resolveMember(r, msg.Member)
	if scopeErr != nil {
		d.sendCrewError(conn, "set", scopeErr)
		return
	}

	member, conflict, err := d.crewSet(identity.Key, msg)
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
	r := who.RequestFromApp(client.selectedProfile())
	identity, scopeErr := d.resolveMember(r, msg.Member)
	if scopeErr != nil {
		d.sendToClient(client, protocol.CrewSetResultMessage{Event: protocol.EventCrewSetResult, RequestID: protocol.Deref(msg.RequestID), Error: protocol.Ptr(scopeErr.Error())})
		return
	}

	if strings.TrimSpace(protocol.Deref(msg.RequestID)) == "" {
		d.sendToClient(client, protocol.CrewSetResultMessage{
			Event: protocol.EventCrewSetResult, Success: false, Conflict: false,
			Error: protocol.Ptr("missing request id"),
		})
		return
	}
	member, conflict, err := d.crewSet(identity.Key, msg)
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

func (d *Daemon) crewSet(key who.MemberKey, msg *protocol.CrewSetMessage) (*protocol.CrewMember, bool, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return nil, false, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return nil, false, err
	}
	for {
		member, doc, err := d.crewMember(key)
		if err != nil {
			return nil, false, err
		}
		if msg.ExpectedRevision != nil && int64(*msg.ExpectedRevision) != doc.Rev {
			wire := d.crewMemberWire(member, doc.Rev)
			return &wire, true, nil
		}
		setting := storeLaunchSetting(msg.LaunchDesktopSetting)
		if msg.LaunchDesktop != nil {
			chosen, err := d.launchDesktopFromRef(d.crewProfileID(member.Key.String()), member.Key.String(), *msg.LaunchDesktop, msg.LaunchDesktopName)
			if err != nil {
				return nil, false, err
			}
			setting = &chosen
		}
		if err := d.applyCrewSettings(&member, msg); err != nil {
			return nil, false, err
		}
		revision, err := d.writeCrewMemberWithLaunch(*schema, member, doc.Rev, setting)
		if err == nil {
			if setting != nil {
				d.publishArrangementChanged(d.crewProfileID(member.Key.String()))
				d.publishMigrationChanged(d.crewProfileID(member.Key.String()))
			}
			wire := d.crewMemberWire(member, revision)
			return &wire, false, nil
		}
		if !docstore.IsConflict(err) {
			return nil, false, err
		}
		if msg.ExpectedRevision != nil {
			current, currentDoc, readErr := d.crewMember(key)
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
	var harness *protocol.Harness
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
	if err := d.validateHarnessModelEffort(agent, member.Model, member.Effort); err != nil {
		return err
	}
	if member.Model == "" || !harness.Discovery || !harness.Available {
		return nil
	}
	catalog, err := d.discoverHarnessModels(context.Background(), agent)
	if err != nil {
		return fmt.Errorf("validate model %q: %w", member.Model, err)
	}
	for _, model := range catalog.Models {
		modelID := model.ID
		if model.Provider != "" {
			modelID = model.Provider + "/" + model.ID
		}
		if modelID != member.Model {
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
