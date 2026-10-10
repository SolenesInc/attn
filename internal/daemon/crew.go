package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) ensureCrewCollections() {
	if d.store == nil {
		return
	}
	for _, schema := range []docstore.CollectionSchema{crew.MembersSchema(), crew.RestartRequestsSchema()} {
		redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
		if err != nil {
			d.logf("crew: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
			continue
		}
		if redeclared {
			d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
		}
	}
}

func (d *Daemon) importCrewHomes() {
	homes := d.registerCrewHomes()
	d.assignCrewProfiles(homes)
	if d.store == nil || d.requireHome(crew.Surface) != nil {
		return
	}
	if err := d.store.PrepareLaunchMigration(); err != nil {
		d.logf("launch desktop migration: %v", err)
	}
}

func (d *Daemon) registerCrewHomes() []crew.Home {
	if d.store == nil {
		return nil
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return nil
	}
	members, err := crew.ScanHomes(filepath.Join(d.dataRoot, crew.HomesDirName), d.logf)
	if err != nil {
		d.logf("crew: %v", err)
		return nil
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.logf("crew: importing homes: %v", err)
		return nil
	}
	registered, _, err := d.readCrewMembersRaw()
	if err != nil && !docstore.IsUndeclaredCollection(err) {
		d.logf("crew: checking registered homes before import: %v", err)
		return nil
	}
	for _, member := range registered {
		if err := d.validateCrewMemberPaths(member); err != nil {
			d.logf("crew: import refused stored member %s: %v", member.Key.String(), err)
		}
	}
	for _, home := range members {
		member := home.Member
		if err := d.validateCrewMemberPaths(member); err != nil {
			d.logf("crew: import refused member %s: %v", member.Key.String(), err)
			continue
		}
		if _, err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			d.logf("crew: importing %s: %v", member.Key.String(), err)
			continue
		}
		d.publishFact(FactCrewRegistered, member.Key.String(), nil)
		d.logf("crew: imported member %s from %s", member.Key.String(), member.HomeDir)
	}
	return members
}

func (d *Daemon) assignCrewProfiles(homes []crew.Home) {
	if d.store == nil || d.requireHome(crew.Surface) != nil {
		return
	}
	members, _, err := d.readCrewMembersRaw()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading members to give them a profile: %v", err)
		}
		return
	}
	if len(members) == 0 {
		return
	}
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		d.logf("crew: members need a profile and none can be given: %v", err)
		return
	}
	for _, member := range members {
		profileID := profile.ID
		for _, home := range homes {
			if home.Member.Key == member.Key && home.ProfileID != "" {
				if _, err := d.store.LiveProfile(home.ProfileID); err == nil {
					profileID = home.ProfileID
				}
				break
			}
		}
		_, err := d.importCrewIdentity(member.Key, profileID)
		if err != nil {
			d.logf("crew: giving %s a profile: %v", member.Key.String(), err)
		}
	}
}

func (d *Daemon) importCrewIdentity(key who.MemberKey, profileID string) (store.CrewIdentity, error) {
	base := crew.NameFromKey(key)
	identity := store.CrewIdentity{Key: key, ProfileID: profileID, Name: base}
	result, err := d.store.EnsureCrewMember(identity)
	var taken *store.CrewNameTakenError
	for number := 2; errors.As(err, &taken); number++ {
		suffix := fmt.Sprintf("-%d", number)
		stem := base
		if len(stem)+len(suffix) > 40 {
			stem = stem[:40-len(suffix)]
		}
		identity.Name = stem + suffix
		d.logf("crew: name %s taken; trying %s for %s", taken.Name, identity.Name, key)
		result, err = d.store.EnsureCrewMember(identity)
	}
	return result, err
}

func (d *Daemon) crewProfileID(memberID string) string {
	key, err := who.ParseMemberKey(memberID)
	if err != nil {
		return ""
	}
	identity, err := d.store.CrewIdentity(key)
	if err != nil {
		d.logf("crew: reading the profile of %s: %v", memberID, err)
	}
	return identity.ProfileID
}

func (d *Daemon) crewCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(crew.Namespace, crew.CollectionMembers)
}

func (d *Daemon) crewRestartRequestsCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(crew.Namespace, crew.CollectionRestartRequests)
}

func (d *Daemon) writeCrewMember(schema docstore.CollectionSchema, member crew.Member, expected int64) (int64, error) {
	return d.writeCrewMemberWithLaunch(schema, member, expected, nil)
}

func (d *Daemon) writeCrewMemberWithLaunch(schema docstore.CollectionSchema, member crew.Member, expected int64, setting *store.LaunchDesktopSetting) (int64, error) {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return 0, err
	}
	body, err := member.Encode()
	if err != nil {
		return 0, err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.Key.String(), false)
	// A member's binding decides which session its seed roles reach, so it changes under the role lock.
	d.lockGardenRoles()
	write := store.DocumentWrite{Schema: schema, ID: member.Key.String(), Body: body, Expected: &expected}
	var written store.DocumentWriteResult
	if setting == nil {
		written, err = d.store.CommitDocumentWrite(write, fact, time.Now())
	} else {
		written, err = d.store.CommitCrewSettings(write, fact, time.Now(), *setting)
	}
	d.unlockGardenRoles()
	if err != nil {
		return 0, err
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.publishFact(FactCrewUpdated, member.Key.String(), nil)
	return written.Rev, nil
}

func (d *Daemon) readCrewMembers() ([]crew.Member, map[string]docstore.Document, error) {
	members, docs, err := d.readCrewMembersRaw()
	if err != nil {
		return nil, nil, err
	}
	for _, member := range members {
		if err := d.validateCrewMemberPaths(member); err != nil {
			return nil, nil, err
		}
	}
	return members, docs, nil
}

func (d *Daemon) resolveCrewMember(stored string) (crew.Member, bool, error) {
	key, err := who.ParseMemberKey(stored)
	if err != nil {
		return crew.Member{}, false, err
	}
	member, _, err := d.crewMember(key)
	return member, err == nil, err
}

func (d *Daemon) readCrewMembersRaw() ([]crew.Member, map[string]docstore.Document, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  crew.Namespace,
		Collection: crew.CollectionMembers,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: false},
		Limit:      docstore.MaxLimit,
	})
	if err != nil {
		return nil, nil, err
	}
	members := make([]crew.Member, 0, len(read.Documents))
	docs := make(map[string]docstore.Document, len(read.Documents))
	for _, doc := range read.Documents {
		member, err := crew.Decode(doc.ID, doc.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("read crew member %s: %w", doc.ID, err)
		}
		members = append(members, member)
		docs[member.Key.String()] = doc
	}
	return members, docs, nil
}

func (d *Daemon) updateCrewMember(key who.MemberKey, mutate func(*crew.Member) (bool, error)) (crew.Member, error) {
	memberID := key.String()
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, err
	}
	const attempts = 3
	for range attempts {
		member, doc, err := d.crewMember(key)
		if err != nil {
			return crew.Member{}, err
		}
		write, err := mutate(&member)
		if err != nil || !write {
			return member, err
		}
		_, err = d.writeCrewMember(*schema, member, doc.Rev)
		if err == nil {
			return member, nil
		}
		if !docstore.IsConflict(err) {
			return crew.Member{}, err
		}
	}
	return crew.Member{}, fmt.Errorf("the registry record for %q was rewritten under all %d attempts to update it; try again", memberID, attempts)
}

func (d *Daemon) crewBindingLive(member crew.Member) bool {
	return member.BindingSession != "" && d.sessionExists(member.BindingSession)
}

func (d *Daemon) liveSessionForTender(tender garden.Tender) (protocol.SessionID, bool) {
	memberID := strings.TrimSpace(tender.Member)
	if memberID != "" {
		member, found, err := d.resolveCrewMember(memberID)
		if err != nil || !found || !d.crewBindingLive(member) {
			return "", false
		}
		return member.BindingSession, true
	}
	if sessionID := protocol.TrimID(tender.Session); sessionID != "" && d.store.Get(sessionID) != nil {
		return sessionID, true
	}
	return "", false
}

func (d *Daemon) claimCrewBinding(key who.MemberKey, sessionID protocol.SessionID) (who.MemberKey, error) {
	memberName := key.String()
	if err := d.requireHome(crew.Surface); err != nil {
		return who.MemberKey{}, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return who.MemberKey{}, err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return who.MemberKey{}, err
		}
		member, ok := memberWithKey(memberName, members)
		if !ok {
			return who.MemberKey{}, fmt.Errorf("no crew member %q is registered; use `attn crew list` to see names", memberName)
		}
		if member.BindingSession == sessionID {

			return member.Key, nil
		}
		if d.crewBindingLive(member) {
			return who.MemberKey{}, fmt.Errorf("%s is already awake in session %s. Wait for it to end, or wake another member",
				d.storedMemberName(member.Key.String()), shortSessionID(member.BindingSession))
		}
		d.releaseCrewBindingsExcept(*schema, members, docs, member.Key.String(), sessionID)

		member.BindingSession = sessionID
		_, err = d.writeCrewMember(*schema, member, docs[member.Key.String()].Rev)
		if err == nil {

			d.invalidateGardenSeedParties("crew bind")
			d.publishFact(FactCrewBound, member.Key.String(), nil)
			d.logf("crew: session %s bound as %s", sessionID, member.Key.String())
			return member.Key, nil
		}
		if !docstore.IsConflict(err) {
			return who.MemberKey{}, err
		}
	}
	return who.MemberKey{}, fmt.Errorf("the registry record for %q was rewritten under all %d attempts to bind it; try again", memberName, attempts)
}

func (d *Daemon) releaseCrewBindingIfSession(sessionID protocol.SessionID) {
	if d.store == nil || protocol.TrimID(sessionID) == "" {
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		return
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to release %s: %v", sessionID, err)
		}
		return
	}
	d.releaseCrewBindingsExcept(*schema, members, docs, "", sessionID)
}

func (d *Daemon) releaseCrewBinding(key who.MemberKey, sessionID protocol.SessionID) (bool, error) {
	memberID := key.String()
	released := false
	_, err := d.updateCrewMember(key, func(member *crew.Member) (bool, error) {
		released = false
		if member.BindingSession != sessionID {
			return false, nil
		}

		member.BindingSession = ""
		released = true
		return true, nil
	})
	if err != nil || !released {
		return released, err
	}
	d.invalidateGardenSeedParties("crew release")
	d.publishFact(FactCrewReleased, memberID, nil)
	d.logf("crew: session %s released %s's binding", sessionID, memberID)
	return true, nil
}

func (d *Daemon) releaseExitedCrewBinding(sessionID protocol.SessionID) {
	// Sessions a stopping daemon kills come back recoverable; the next daemon releases and reports them.
	if d.stopping() {
		return
	}
	member, bound := d.crewMemberForSession(sessionID)
	if !bound {
		return
	}
	released, err := d.releaseCrewBinding(member.Key, sessionID)
	if err != nil {
		d.logf("crew: releasing exited session %s from %s: %v", sessionID, member.Key.String(), err)
		return
	}
	if !released {
		return
	}
	d.noteCrewExitedSession(member.Key.String(), sessionID)
	restart, pending := pendingCrewRestartFor(member, sessionID)
	if !pending || restart.LetterPath != "" || member.LetterSession == sessionID {
		return
	}
	if d.stopping() {
		// A stopping daemon cannot run the successor; the next daemon completes the queued restart.
		return
	}
	if _, err := d.crewRestart(member.Key, restart.RequestID, nil, nil); err != nil {
		d.logf("crew: restarting %s after session %s exited: %v", member.Key.String(), sessionID, err)
	}
}

func (d *Daemon) noteCrewExitedSession(memberID string, sessionID protocol.SessionID) {
	d.crewExitedMu.Lock()
	defer d.crewExitedMu.Unlock()
	if d.crewExitedSessions == nil {
		d.crewExitedSessions = make(map[string]string)
	}
	d.crewExitedSessions[memberID] = string(sessionID)
}

func (d *Daemon) takeCrewExitedSession(memberID string) string {
	d.crewExitedMu.Lock()
	defer d.crewExitedMu.Unlock()
	sessionID := d.crewExitedSessions[memberID]
	delete(d.crewExitedSessions, memberID)
	return sessionID
}

func (d *Daemon) releaseCrewBindingsExcept(schema docstore.CollectionSchema, members []crew.Member, docs map[string]docstore.Document, keepID string, sessionID protocol.SessionID) {
	for _, member := range members {
		if member.BindingSession != sessionID || member.Key.String() == keepID {
			continue
		}

		member.BindingSession = ""
		if _, err := d.writeCrewMember(schema, member, docs[member.Key.String()].Rev); err != nil {
			d.logf("crew: releasing %s's binding for session %s: %v", member.Key.String(), sessionID, err)
			continue
		}
		d.invalidateGardenSeedParties("crew release")
		d.publishFact(FactCrewReleased, member.Key.String(), nil)
		d.logf("crew: session %s released %s's binding", sessionID, member.Key.String())
	}
}

func (d *Daemon) crewMembersBySession() map[protocol.SessionID]string {
	if d.store == nil {
		return nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for broadcast: %v", err)
		}
		return nil
	}
	var out map[protocol.SessionID]string
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		if out == nil {
			out = make(map[protocol.SessionID]string)
		}
		out[member.BindingSession] = member.Key.String()
	}
	return out
}

func (d *Daemon) crewMemberBoundTo(sessionID protocol.SessionID) string {
	if d.store == nil || protocol.TrimID(sessionID) == "" {
		return ""
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for session %s: %v", sessionID, err)
		}
		return ""
	}
	for _, member := range members {
		if member.BindingSession == sessionID && d.crewBindingLive(member) {
			return member.Key.String()
		}
	}
	return ""
}

func (d *Daemon) decorateCrewMember(session *protocol.Session, bindings who.Bindings) {
	if session == nil {
		return
	}
	party, _ := bindings.PartyOf(session.ID)
	if member, ok := party.Member(); ok {
		session.CrewMember = protocol.Ptr(member.String())
		return
	}
	session.CrewMember = nil
}

func (d *Daemon) resolveTenderMember(memberName string, sessionID protocol.SessionID, profileID ...string) string {
	memberName = strings.TrimSpace(memberName)
	if memberName == "" {
		return d.crewMemberBoundTo(sessionID)
	}
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		d.logf("crew tender bindings: %v", bindingsErr)
		return memberName
	}
	r, err := d.requestFromMessage(protocol.Ptr(sessionID), protocol.Ptr(firstProfile(profileID)), b)
	if err == nil {
		if member, found, err := d.store.CrewNamed(r.ProfileID(), memberName); err == nil && found {
			return member.Key.String()
		}
	}
	return memberName
}

func (d *Daemon) sendCrewError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("crew %s: %v", verb, err))
}

func (d *Daemon) crewMemberWire(member crew.Member, revision int64) protocol.CrewMember {
	wire := protocol.CrewMember{
		Key:           member.Key.String(),
		Name:          d.memberName(member.Key),
		ProfileID:     d.crewProfileID(member.Key.String()),
		Revision:      int(revision),
		CharterPath:   member.CharterPath,
		HomeDir:       member.HomeDir,
		ResolvedAgent: member.LaunchAgent(),
	}
	if item, err := d.store.LaunchDesktopItem("crew", member.Key.String()); err == nil {
		setting := protocolLaunchItem(item).Setting
		wire.LaunchDesktop = &setting
		wire.ProfileName = protocol.Ptr(item.ProfileName)
	} else {
		d.logf("crew launch desktop %s: %v", member.Key.String(), err)
	}
	if member.CWD != "" {
		wire.Cwd = protocol.Ptr(member.CWD)
	}
	if member.Agent != "" {
		wire.Agent = protocol.Ptr(member.Agent)
	}
	if member.Model != "" {
		wire.Model = protocol.Ptr(member.Model)
	}
	if member.Effort != "" {
		wire.Effort = protocol.Ptr(member.Effort)
	}
	if model := d.crewWakeModel(member, member.LaunchAgent()); model != nil {
		wire.ResolvedModel = model
	}
	if effort := d.crewWakeEffort(member, member.LaunchAgent()); effort != nil {
		wire.ResolvedEffort = effort
	}
	wire.AwarenessDirs = append([]string{}, member.AwarenessDirs...)
	if d.crewBindingLive(member) {
		wire.BindingSession = protocol.Ptr(member.BindingSession)
	}
	if member.Restart != nil {
		wire.Restart = crewRestartWire(member.Restart)
	}
	return wire
}

func (d *Daemon) crewRoster(profileID string) []protocol.CrewMember {
	out := []protocol.CrewMember{}
	if d.store == nil || d.requireHome(crew.Surface) != nil {
		return out
	}
	identities, err := d.store.CrewRoster(profileID)
	if err != nil {
		d.logf("crew roster: %v", err)
		return out
	}
	for _, identity := range identities {
		member, doc, err := d.crewMember(identity.Key)
		if err != nil {
			d.logf("crew roster: %v", err)
			return nil
		}
		out = append(out, d.crewMemberWire(member, doc.Rev))
	}
	return out
}

func (d *Daemon) crewProfileSnapshot(profileID string) *protocol.CrewUpdatedMessage {
	return &protocol.CrewUpdatedMessage{Event: protocol.EventCrewUpdated, ProfileID: profileID, Members: d.crewRoster(profileID)}
}
func (d *Daemon) projectCrewRoster() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotCrew, func() {
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profile := client.selectedProfile()
			data, ok := snapshots[profile]
			if !ok {
				var err error
				data, err = json.Marshal(d.crewProfileSnapshot(profile))
				if err != nil {
					d.logf("crew snapshot: %v", err)
					return
				}
				snapshots[profile] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}
func (d *Daemon) sendProfileSnapshots(client *wsClient) {
	d.sendGardenProfile(client)
	d.sendToClient(client, d.crewProfileSnapshot(client.selectedProfile()))
}
func (d *Daemon) handleCrewList(conn net.Conn, msg *protocol.CrewListMessage) {
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	b, bindingsErr := d.bindings()
	if bindingsErr != nil {
		d.sendError(conn, bindingsErr.Error())
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	identities, err := d.store.CrewRoster(r.ProfileID())
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	for _, identity := range identities {
		if _, _, err := d.crewMember(identity.Key); err != nil {
			d.sendCrewError(conn, "list", err)
			return
		}
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewListResult: &protocol.CrewListResult{Members: d.crewRoster(r.ProfileID())}})
}
func memberWithKey(key string, members []crew.Member) (crew.Member, bool) {
	for _, member := range members {
		if member.Key.String() == key {
			return member, true
		}
	}
	return crew.Member{}, false
}

func (d *Daemon) promoteRetainedMemberLaunch(session *protocol.Session, previousRunSessions map[protocol.SessionID]struct{}) {
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	if !d.store.SessionLaunchedAt(session.ID).IsZero() {
		return
	}
	entry := d.store.SessionLedgerEntry(session.ID)
	if entry == nil || entry.MemberKey == nil {
		return
	}
	launchedAt, err := time.Parse(time.RFC3339Nano, session.StateSince)
	if err != nil {
		d.logf("crew: retained launch %s has unreadable state_since %q: %v", session.ID, session.StateSince, err)
		return
	}
	key, err := who.ParseMemberKey(*entry.MemberKey)
	if err != nil {
		d.logf("crew: retained launch %s has invalid member key: %v", session.ID, err)
		return
	}
	member, _, err := d.crewMember(key)
	if err != nil {
		d.logf("crew: retained launch %s: %v", session.ID, err)
		return
	}
	if member.BindingSession != session.ID && d.crewBindingLive(member) {
		d.store.SetSessionLaunchedAt(session.ID, launchedAt, who.MemberKey{})
		return
	}
	latest, err := d.store.MemberLatestSession(key)
	if err != nil {
		d.logf("crew: latest session for %s during recovery: %v", key, err)
		return
	}
	if latest != "" && latest != session.ID {
		if _, previous := previousRunSessions[latest]; !previous {
			d.store.SetSessionLaunchedAt(session.ID, launchedAt, who.MemberKey{})
			return
		}
	}
	d.store.SetSessionLaunchedAt(session.ID, launchedAt, key)
}
