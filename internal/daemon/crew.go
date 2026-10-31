package daemon

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) ensureCrewCollections() {
	if d.store == nil {
		return
	}
	schema := crew.MembersSchema()
	redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
	if err != nil {
		d.logf("crew: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
		return
	}
	if redeclared {
		d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
	}
}

func (d *Daemon) importCrewHomes() {
	d.registerCrewHomes()
	d.assignCrewProfiles()
	if d.store == nil {
		return
	}
	if err := d.store.PrepareLaunchMigration(); err != nil {
		d.logf("launch desktop migration: %v", err)
	}
}

func (d *Daemon) registerCrewHomes() {
	if d.store == nil {
		return
	}
	members, err := crew.ScanHomes(filepath.Join(d.dataRoot, crew.HomesDirName), d.logf)
	if err != nil {
		d.logf("crew: %v", err)
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.logf("crew: importing homes: %v", err)
		return
	}
	for _, member := range members {
		if err := d.requireCrewFiles(member); err != nil {
			d.logf("crew: import refused member %s: %v", member.ID, err)
			continue
		}
		if _, err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			d.logf("crew: importing %s: %v", member.ID, err)
			continue
		}
		d.publishFact(FactCrewRegistered, member.ID, nil)
		d.logf("crew: imported member %s from %s", member.ID, member.HomeDir(d.dataRoot))
	}
}

func (d *Daemon) assignCrewProfiles() {
	if d.store == nil {
		return
	}
	members, _, err := d.readCrewMembers()
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
		if member.ProfileID != "" {
			continue
		}
		identity, err := d.store.EnsureCrewMember(member.ID, profile.ID)
		if err != nil {
			d.logf("crew: giving member %s a profile and a name: %v", member.ID, err)
			continue
		}
		d.logf("crew: member %s is %s in profile %s", member.ID, identity.Name, identity.ProfileID)
	}
}

func (d *Daemon) crewProfileID(memberID string) string {
	profileID, err := d.store.CrewProfile(memberID)
	if err != nil {
		d.logf("crew: reading the profile of %s: %v", memberID, err)
	}
	return profileID
}

func (d *Daemon) crewCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(crew.Namespace, crew.CollectionMembers)
}

func (d *Daemon) writeCrewMember(schema docstore.CollectionSchema, member crew.Member, expected int64) (int64, error) {
	return d.writeCrewMemberWithLaunch(schema, member, expected, nil)
}

func (d *Daemon) writeCrewMemberWithLaunch(schema docstore.CollectionSchema, member crew.Member, expected int64, setting *store.LaunchDesktopSetting) (int64, error) {
	body, err := member.Encode()
	if err != nil {
		return 0, err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	// A member's binding decides which session its seed roles reach, so it changes under the role lock.
	d.lockGardenRoles()
	write := store.DocumentWrite{Schema: schema, ID: member.ID, Body: body, Expected: &expected}
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
	d.publishFact(FactCrewUpdated, member.ID, nil)
	return written.Rev, nil
}

func (d *Daemon) crewMember(key string) (crew.Member, docstore.Document, error) {
	members, docs, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, docstore.Document{}, err
	}
	member, ok := crew.Keyed(key, members)
	if !ok {
		return crew.Member{}, docstore.Document{}, fmt.Errorf("no crew member has the key %q; `attn crew list` names the roster", key)
	}
	return member, docs[member.ID], nil
}

func (d *Daemon) crewMemberIn(profileID, ref string) (crew.Member, bool, error) {
	members, _, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, false, err
	}
	inProfile := members[:0:0]
	for _, member := range members {
		if member.ProfileID == profileID {
			inProfile = append(inProfile, member)
		}
	}
	if strings.EqualFold(strings.TrimSpace(ref), crew.ChiefAlias) {
		for _, member := range inProfile {
			if member.Chief {
				return member, true, nil
			}
		}
	}
	member, ok := crew.Resolve(ref, inProfile)
	return member, ok, nil
}

func (d *Daemon) crewMemberNamed(profileID, ref string) (crew.Member, error) {
	member, found, err := d.crewMemberIn(profileID, ref)
	if err != nil || found {
		return member, err
	}
	profile, err := d.store.GetProfile(profileID)
	if err != nil {
		return crew.Member{}, err
	}
	return crew.Member{}, fmt.Errorf("no crew member named %q in profile %q; `attn crew list` names the roster", strings.TrimSpace(ref), profile.Name)
}

type crewAsker struct {
	session   protocol.SessionID
	profileID string
}

func askerOf(session *protocol.SessionID, profileID *string) crewAsker {
	return crewAsker{session: protocol.Deref(session), profileID: protocol.Deref(profileID)}
}

func (d *Daemon) crewAskerProfile(asker crewAsker) (string, error) {
	profile, err := d.resolveGardenProfile(asker.session, asker.profileID, "")
	return profile.ID, err
}

func (d *Daemon) crewAskedMember(asker crewAsker, ref string) (crew.Member, error) {
	profileID, err := d.crewAskerProfile(asker)
	if err != nil {
		return crew.Member{}, err
	}
	return d.crewMemberNamed(profileID, ref)
}

func (d *Daemon) readCrewMembers() ([]crew.Member, map[string]docstore.Document, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  crew.Namespace,
		Collection: crew.CollectionMembers,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: false},
		Limit:      docstore.MaxLimit,
	})
	if err != nil {
		return nil, nil, err
	}
	identities, err := d.store.CrewIdentities()
	if err != nil {
		return nil, nil, err
	}
	members := make([]crew.Member, 0, len(read.Documents))
	docs := make(map[string]docstore.Document, len(read.Documents))
	for _, doc := range read.Documents {
		member, err := crew.Decode(doc.Body)
		if err != nil {
			d.logf("crew: member %s has an unreadable record: %v", doc.ID, err)
			continue
		}
		if err := crew.ValidateID(member.ID); err != nil || member.ID != doc.ID {
			d.logf("crew: member %s has an invalid identity %q: %v", doc.ID, member.ID, err)
			continue
		}
		if member.Key, err = who.ParseMemberKey(member.ID); err != nil {
			d.logf("crew: member %s has an invalid key: %v", doc.ID, err)
			continue
		}
		identity := identities[member.ID]
		member.Name, member.ProfileID, member.Retired, member.Chief = identity.Name, identity.ProfileID, identity.Retired, identity.Chief
		if member.Name == "" {
			member.Name = crew.NameFromKey(member.ID)
		}
		members = append(members, member)
		docs[member.ID] = doc
	}
	return members, docs, nil
}

func (d *Daemon) updateCrewMember(memberID string, mutate func(*crew.Member) (bool, error)) (crew.Member, error) {
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return crew.Member{}, err
		}
		member, ok := crew.Keyed(memberID, members)
		if !ok {
			return crew.Member{}, fmt.Errorf("no crew member has the key %q", memberID)
		}
		write, err := mutate(&member)
		if err != nil || !write {
			return member, err
		}
		_, err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
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

func (d *Daemon) liveSessionOf(p who.Party) (protocol.SessionID, bool) {
	if key, ok := p.Member(); ok {
		member, _, err := d.crewMember(key.String())
		if err != nil || !d.crewBindingLive(member) {
			return "", false
		}
		return member.BindingSession, true
	}
	if sessionID, ok := p.Session(); ok && d.store.Get(sessionID) != nil {
		return sessionID, true
	}
	return "", false
}

func (d *Daemon) claimCrewBinding(key string, sessionID protocol.SessionID) (string, error) {
	schema, err := d.crewCollection()
	if err != nil {
		return "", err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return "", err
		}
		member, ok := crew.Keyed(key, members)
		if !ok {
			return "", fmt.Errorf("no crew member has the key %q; `attn crew list` names the roster", key)
		}
		if member.BindingSession == sessionID {

			return member.ID, nil
		}
		if d.crewBindingLive(member) {
			return "", fmt.Errorf("%s is already awake in session %s; two agents with the same identity never run at once — wait for that day to end, or wake another member",
				member.Name, shortSessionID(member.BindingSession))
		}
		d.releaseCrewBindingsExcept(*schema, members, docs, member.ID, sessionID)

		member.BindingSession = sessionID
		_, err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {

			d.invalidateGardenSeedParties("crew bind")
			d.publishFact(FactCrewBound, member.ID, nil)
			d.logf("crew: session %s bound as %s", sessionID, member.Name)
			return member.ID, nil
		}
		if !docstore.IsConflict(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("the registry record for %q was rewritten under all %d attempts to bind it; try again", key, attempts)
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

func (d *Daemon) releaseCrewBinding(memberID string, sessionID protocol.SessionID) (bool, error) {
	released := false
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
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
	released, err := d.releaseCrewBinding(member.ID, sessionID)
	if err != nil {
		d.logf("crew: releasing exited session %s from %s: %v", sessionID, member.Name, err)
		return
	}
	if !released {
		return
	}
	d.noteCrewExitedSession(member.ID, sessionID)
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
		if member.BindingSession != sessionID || member.ID == keepID {
			continue
		}

		member.BindingSession = ""
		if _, err := d.writeCrewMember(schema, member, docs[member.ID].Rev); err != nil {
			d.logf("crew: releasing %s's binding for session %s: %v", member.Name, sessionID, err)
			continue
		}
		d.invalidateGardenSeedParties("crew release")
		d.publishFact(FactCrewReleased, member.ID, nil)
		d.logf("crew: session %s released %s's binding", sessionID, member.Name)
	}
}

func (d *Daemon) crewMembersBySession() map[protocol.SessionID]crew.Member {
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
	var out map[protocol.SessionID]crew.Member
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		if out == nil {
			out = make(map[protocol.SessionID]crew.Member)
		}
		out[member.BindingSession] = member
	}
	return out
}

func (d *Daemon) crewMemberBoundTo(sessionID protocol.SessionID) string {
	member, _ := d.crewMemberBoundToSession(sessionID)
	return member.ID
}

func (d *Daemon) crewMemberBoundToSession(sessionID protocol.SessionID) (crew.Member, bool) {
	if d.store == nil || protocol.TrimID(sessionID) == "" {
		return crew.Member{}, false
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for session %s: %v", sessionID, err)
		}
		return crew.Member{}, false
	}
	for _, member := range members {
		if member.BindingSession == sessionID && d.crewBindingLive(member) {
			return member, true
		}
	}
	return crew.Member{}, false
}

func (d *Daemon) crewSessionBoundTo(memberID string) (protocol.SessionID, error) {
	if d.store == nil || strings.TrimSpace(memberID) == "" {
		return "", nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		return "", err
	}
	member, ok := crew.Keyed(memberID, members)
	if !ok || !d.crewBindingLive(member) {
		return "", nil
	}
	return member.BindingSession, nil
}

func (d *Daemon) decorateCrewMember(session *protocol.Session, membersBySession map[protocol.SessionID]crew.Member) {
	if session == nil {
		return
	}
	if member, bound := membersBySession[session.ID]; bound {
		session.CrewMember, session.CrewMemberName = protocol.Ptr(member.ID), protocol.Ptr(member.Name)
		return
	}
	session.CrewMember, session.CrewMemberName = nil, nil
}

func (d *Daemon) sendCrewError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("crew %s: %v", verb, err))
}

func (d *Daemon) crewMemberWire(member crew.Member, revision int64) protocol.CrewMember {
	wire := protocol.CrewMember{
		ID:            member.ID,
		Name:          member.Name,
		Retired:       member.Retired,
		ProfileID:     member.ProfileID,
		Revision:      int(revision),
		CharterPath:   member.CharterPath(d.dataRoot),
		HomeDir:       member.HomeDir(d.dataRoot),
		ResolvedAgent: member.LaunchAgent(),
	}
	if item, err := d.store.LaunchDesktopItem("crew", member.ID); err == nil {
		setting := protocolLaunchItem(item).Setting
		wire.LaunchDesktop = &setting
		wire.ProfileName = protocol.Ptr(item.ProfileName)
	} else {
		d.logf("crew launch desktop %s: %v", member.ID, err)
	}
	if member.Chief {
		wire.Chief = protocol.Ptr(true)
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
		if d.crewSwitchDue(member, member.BindingSession) {
			wire.SwitchPending = protocol.Ptr(true)
		}
	}
	return wire
}

func (d *Daemon) crewForBroadcast() []protocol.CrewMember {
	if d.store == nil {
		return nil
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for broadcast: %v", err)
		}
		return nil
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		if member.Retired {
			continue
		}
		out = append(out, d.crewMemberWire(member, docs[member.ID].Rev))
	}
	return out
}

func (d *Daemon) projectCrewRoster() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotCrew, func() {
		members := d.crewForBroadcast()
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.CrewUpdatedMessage{
			Event:   protocol.EventCrewUpdated,
			Members: members,
		})
	})
}

func (d *Daemon) handleCrewList(conn net.Conn, msg *protocol.CrewListMessage) {
	profileID, err := d.crewAskerProfile(askerOf(msg.SourceSessionID, msg.ProfileID))
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		if member.ProfileID != profileID || (member.Retired && !protocol.Deref(msg.IncludeRetired)) {
			continue
		}
		out = append(out, d.crewMemberWire(member, docs[member.ID].Rev))
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		CrewListResult: &protocol.CrewListResult{Members: out},
	})
}

func (d *Daemon) crewMemberName(key string) string {
	identities, err := d.store.CrewIdentities()
	if err != nil {
		d.logf("crew: reading the name of %s: %v", key, err)
	}
	if identity, ok := identities[key]; ok {
		return identity.Name
	}
	return crew.NameFromKey(key)
}

func (d *Daemon) recordMemberSession(key string, sessionID protocol.SessionID) {
	if err := d.store.RecordMemberSession(key, sessionID); err != nil {
		d.logf("crew: recording session %s as %s's: %v", sessionID, key, err)
	}
}

func (d *Daemon) crewMemberKeyed(text string) (crew.Member, bool) {
	members, _, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, false
	}
	return crew.Keyed(strings.TrimPrefix(strings.TrimSpace(text), crew.KeyRefPrefix), members)
}
