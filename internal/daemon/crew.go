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
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Runs on every daemon, home or outpost, because a declaration is schema and
// not state: `attn enrollment leave` then works at once.
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

// Files are canonical: the import records where a home lives, never what it
// says, and the write is create-only so re-running costs nothing.
func (d *Daemon) importCrewHomes() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(crew.Surface); err != nil {
		// An outpost imports nothing; not an error — startup is not a crew ask.
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
	registered, _, err := d.readCrewMembersRaw()
	if err != nil && !docstore.IsUndeclaredCollection(err) {
		d.logf("crew: checking registered homes before import: %v", err)
		return
	}
	for _, member := range registered {
		if err := d.validateCrewMemberPaths(member); err != nil {
			d.logf("crew: import refused stored member %s: %v", crew.DisplayName(member.ID), err)
		}
	}
	for _, member := range members {
		if err := d.validateCrewMemberPaths(member); err != nil {
			d.logf("crew: import refused member %s: %v", crew.DisplayName(member.ID), err)
			continue
		}
		if err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			d.logf("crew: importing %s: %v", crew.DisplayName(member.ID), err)
			continue
		}
		d.publishFact(FactCrewRegistered, member.ID, nil)
		d.logf("crew: imported member %s from %s", crew.DisplayName(member.ID), member.HomeDir)
	}
}

func (d *Daemon) crewCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(crew.Namespace, crew.CollectionMembers)
}

func (d *Daemon) writeCrewMember(schema docstore.CollectionSchema, member crew.Member, expected int64) error {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return err
	}
	body, err := member.Encode()
	if err != nil {
		return err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: member.ID, Body: body, Expected: &expected,
	}, fact, time.Now())
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	return nil
}

// docstore.MaxLimit is not a bound anything real approaches. Measured
// 2026-08-14 at a three-member roster: 25µs on an M5.
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

func (d *Daemon) resolveCrewMember(address string) (crew.Member, bool, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, false, err
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, false, err
	}
	member, ok := crew.Resolve(address, members)
	return member, ok, nil
}

// Reserved for startup import, where invalid copied rows must be enumerated.
// Every operational read goes through readCrewMembers and its path fence.
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
		member, err := crew.Decode(doc.Body)
		if err != nil {
			// One unreadable record must not blank the roster; name it and go on.
			d.logf("crew: member %s has an unreadable record: %v", doc.ID, err)
			continue
		}
		members = append(members, member)
		docs[member.ID] = doc
	}
	return members, docs, nil
}

// mutate returns false to abandon the write without an error. Three attempts is
// a tripwire; two writers contending is one retry.
func (d *Daemon) updateCrewMember(memberID string, mutate func(*crew.Member) (bool, error)) (crew.Member, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, err
	}
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
		member, ok := crew.Resolve(memberID, members)
		if !ok {
			return crew.Member{}, fmt.Errorf("no crew member %q is registered", memberID)
		}
		write, err := mutate(&member)
		if err != nil || !write {
			return member, err
		}
		err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {
			return member, nil
		}
		if !docstore.IsConflict(err) {
			return crew.Member{}, err
		}
	}
	return crew.Member{}, fmt.Errorf("the registry record for %q was rewritten under all %d attempts to update it; try again", memberID, attempts)
}

// Whether a stored binding still binds is judged at read: a non-empty session
// the daemon still knows.
func (d *Daemon) crewBindingLive(member crew.Member) bool {
	return member.BindingSession != "" && d.sessionExists(member.BindingSession)
}

func (d *Daemon) liveSessionForTender(tender garden.Tender) (string, bool) {
	if sessionID := strings.TrimSpace(tender.Session); sessionID != "" && d.store.Get(sessionID) != nil {
		return sessionID, true
	}
	memberID := strings.TrimSpace(tender.Member)
	if memberID == "" {
		return "", false
	}
	member, found, err := d.resolveCrewMember(memberID)
	if err != nil || !found || !d.crewBindingLive(member) {
		return "", false
	}
	return member.BindingSession, true
}

func (d *Daemon) migrateCrewTicketIdentity(memberID string, sessionIDs ...string) error {
	identity := store.TicketMemberIdentity(memberID)
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			continue
		}
		if err := d.store.MigrateTicketIdentity(sessionID, identity, time.Now()); err != nil {
			return fmt.Errorf("carry session %s's ticket participation into %s: %w", shortSessionID(sessionID), identity, err)
		}
	}
	return nil
}

// claim/release cannot cover a daemon upgrade whose live binding survives a
// restart.
func (d *Daemon) migrateCrewTicketIdentities() error {
	members, _, err := d.readCrewMembers()
	if err != nil {
		return err
	}
	for _, member := range members {
		if err := d.migrateCrewTicketIdentity(member.ID, member.BindingSession, member.LetterSession); err != nil {
			return err
		}
	}
	return nil
}

// Refuses an unregistered name, and a member whose current day is still live: two agents
// with the same identity never run at once. Three attempts is a tripwire.
func (d *Daemon) claimCrewBinding(memberName, sessionID string) (string, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return "", err
	}
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
		member, ok := crew.Resolve(memberName, members)
		if !ok {
			return "", fmt.Errorf("no crew member %q is registered; `attn crew list` names the roster", memberName)
		}
		if member.BindingSession == sessionID {
			if err := d.migrateCrewTicketIdentity(member.ID, sessionID); err != nil {
				return "", err
			}
			return member.ID, nil
		}
		if d.crewBindingLive(member) {
			return "", fmt.Errorf("%s is already awake in session %s; two agents with the same identity never run at once — wait for that day to end, or wake another member",
				crew.DisplayName(member.ID), shortSessionID(member.BindingSession))
		}
		// Past every refusal, so the claim is going to land: a refused claim never
		// reaches here and leaves the session the member it already was.
		d.releaseCrewBindingsExcept(*schema, members, docs, member.ID, sessionID)
		previousSessionID := member.BindingSession
		if err := d.migrateCrewTicketIdentity(member.ID, previousSessionID); err != nil {
			return "", err
		}
		member.BindingSession = sessionID
		err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {
			if err := d.migrateCrewTicketIdentity(member.ID, sessionID); err != nil {
				return "", err
			}
			d.publishFact(FactCrewBound, member.ID, nil)
			d.logf("crew: session %s bound as %s", sessionID, crew.DisplayName(member.ID))
			return member.ID, nil
		}
		if !docstore.IsConflict(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("the registry record for %q was rewritten under all %d attempts to bind it; try again", memberName, attempts)
}

// A path that misses it costs only a stale record the liveness judgment already
// ignores, so it reads before it fences.
func (d *Daemon) releaseCrewBindingIfSession(sessionID string) {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
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

// Returns failures, unlike the broad teardown: a wake must not launch a second
// day until the dead day seat is known free.
func (d *Daemon) releaseCrewBinding(memberID, sessionID string) (bool, error) {
	released := false
	_, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		released = false
		if member.BindingSession != sessionID {
			return false, nil
		}
		if err := d.migrateCrewTicketIdentity(member.ID, sessionID); err != nil {
			return false, err
		}
		member.BindingSession = ""
		released = true
		return true, nil
	})
	if err != nil || !released {
		return released, err
	}
	d.publishFact(FactCrewReleased, memberID, nil)
	d.logf("crew: session %s released %s's binding", sessionID, crew.DisplayName(memberID))
	return true, nil
}

func (d *Daemon) releaseExitedCrewBinding(sessionID string) {
	member, bound := d.crewMemberForSession(sessionID)
	if !bound {
		return
	}
	released, err := d.releaseCrewBinding(member.ID, sessionID)
	if err != nil {
		d.logf("crew: releasing exited session %s from %s: %v", sessionID, crew.DisplayName(member.ID), err)
		return
	}
	if released {
		d.noteCrewExitedSession(member.ID, sessionID)
	}
}

func (d *Daemon) noteCrewExitedSession(memberID, sessionID string) {
	d.crewExitedMu.Lock()
	defer d.crewExitedMu.Unlock()
	if d.crewExitedSessions == nil {
		d.crewExitedSessions = make(map[string]string)
	}
	d.crewExitedSessions[memberID] = sessionID
}

func (d *Daemon) takeCrewExitedSession(memberID string) string {
	d.crewExitedMu.Lock()
	defer d.crewExitedMu.Unlock()
	sessionID := d.crewExitedSessions[memberID]
	delete(d.crewExitedSessions, memberID)
	return sessionID
}

func (d *Daemon) releaseCrewBindingsExcept(schema docstore.CollectionSchema, members []crew.Member, docs map[string]docstore.Document, keepID, sessionID string) {
	for _, member := range members {
		if member.BindingSession != sessionID || member.ID == keepID {
			continue
		}
		if err := d.migrateCrewTicketIdentity(member.ID, sessionID); err != nil {
			d.logf("crew: keeping %s's stale binding for session %s because ticket participation did not move: %v", crew.DisplayName(member.ID), sessionID, err)
			continue
		}
		member.BindingSession = ""
		if err := d.writeCrewMember(schema, member, docs[member.ID].Rev); err != nil {
			d.logf("crew: releasing %s's binding for session %s: %v", crew.DisplayName(member.ID), sessionID, err)
			continue
		}
		d.publishFact(FactCrewReleased, member.ID, nil)
		d.logf("crew: session %s released %s's binding", sessionID, crew.DisplayName(member.ID))
	}
}

// Empty, never an error, when the roster is unreadable: decoration must not fail
// a broadcast.
func (d *Daemon) crewMembersBySession() map[string]string {
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
	var out map[string]string
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[member.BindingSession] = member.ID
	}
	return out
}

// Read the roster rather than the session record: CrewMember is a broadcast
// decoration, so it is nil on everything d.store.Get returns.
func (d *Daemon) crewMemberBoundTo(sessionID string) string {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
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
			return member.ID
		}
	}
	return ""
}

// Cleared otherwise so it round-trips as an omitted field.
func (d *Daemon) decorateCrewMember(session *protocol.Session, membersBySession map[string]string) {
	if session == nil {
		return
	}
	if member := membersBySession[session.ID]; member != "" {
		session.CrewMember = protocol.Ptr(member)
		return
	}
	session.CrewMember = nil
}

// An unregistered name passes through: the registry is never a requirement.
func (d *Daemon) resolveTenderMember(memberName, sessionID string) string {
	memberName = strings.TrimSpace(memberName)
	if memberName == "" {
		return d.crewMemberBoundTo(sessionID)
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to resolve %q: %v", memberName, err)
		}
		return memberName
	}
	if member, ok := crew.Resolve(memberName, members); ok {
		return member.ID
	}
	return memberName
}

func (d *Daemon) sendCrewError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("crew %s: %v", verb, err))
}

func (d *Daemon) crewMemberWire(member crew.Member) protocol.CrewMember {
	wire := protocol.CrewMember{
		ID:          member.ID,
		CharterPath: member.CharterPath,
		HomeDir:     member.HomeDir,
	}
	if member.CWD != "" {
		wire.Cwd = protocol.Ptr(member.CWD)
	}
	// Always the resolved answer, never the stored blank: a reader asking what a
	// member runs on must not have to know the default.
	wire.Agent = protocol.Ptr(member.LaunchAgent())
	if member.Model != "" {
		wire.Model = protocol.Ptr(member.Model)
	}
	wire.AwarenessDirs = append([]string{}, member.AwarenessDirs...)
	// Only a binding that still binds reaches the wire; liveness is judged here.
	if d.crewBindingLive(member) {
		wire.BindingSession = protocol.Ptr(member.BindingSession)
	}
	return wire
}

func (d *Daemon) crewForBroadcast() []protocol.CrewMember {
	if d.store == nil {
		return nil
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for broadcast: %v", err)
		}
		return nil
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		out = append(out, d.crewMemberWire(member))
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

func (d *Daemon) handleCrewList(conn net.Conn, _ *protocol.CrewListMessage) {
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		out = append(out, d.crewMemberWire(member))
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		CrewListResult: &protocol.CrewListResult{Members: out},
	})
}
