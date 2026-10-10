package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

var keyShapedInput = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func (d *Daemon) bindings() (who.Bindings, error) {
	bound := make(map[who.MemberKey]protocol.SessionID)
	unreadable := make(map[who.MemberKey]error)
	status, err := d.enrollmentStatus()
	if err != nil {
		return who.Bindings{}, err
	}
	if status.IsHome() {
		documents, err := d.readCrewMemberDocuments()
		if err != nil {
			return who.Bindings{}, err
		}
		for _, doc := range documents {
			key, err := who.ParseMemberKey(doc.ID)
			if err != nil {
				d.reportCrewDocument(doc, err)
				continue
			}
			var binding struct {
				Session protocol.SessionID `json:"binding_session"`
			}
			bindingErr := json.Unmarshal(doc.Body, &binding)
			if bindingErr == nil && binding.Session != "" {
				bound[key] = binding.Session
			}
			_, err = crew.Decode(doc.ID, doc.Body)
			d.reportCrewDocument(doc, err)
			if err != nil {
				unreadable[key] = fmt.Errorf("crew member %s (%s) is unreadable: %w", d.memberName(key), key, err)
				if bindingErr != nil || binding.Session == "" || !d.sessionExists(binding.Session) {
					id, latestErr := d.store.MemberLatestSession(key)
					if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
						return who.Bindings{}, latestErr
					}
					if d.store.Get(id) != nil {
						bound[key] = id
					}
				}
			}
		}
	}
	return who.NewBindings(d.sessionFacts, bound, unreadable), nil
}
func (d *Daemon) broadcastBindings() who.Bindings {
	b, err := d.bindings()
	if err != nil {
		d.logf("session broadcast bindings: %v", err)
		return who.NewBindings(d.sessionFacts, nil, nil)
	}
	return b
}
func (d *Daemon) sessionFacts(id protocol.SessionID) (string, bool) {
	if d.hubManager != nil {
		if s := d.hubManager.RemoteSession(id); s != nil {
			return s.ProfileID, true
		}
	}
	profile, err := d.store.GardenSessionProfileID(id)
	if err != nil {
		d.logf("who: session %s profile: %v", id, err)
		return "", false
	}
	return profile, d.sessionExists(id)
}
func (d *Daemon) requestFromSession(id protocol.SessionID, b who.Bindings) (who.Requester, error) {
	s, code := d.resolveSessionByIDOrPrefix(string(id), "")
	if s == nil {
		if code == "ambiguous_session" {
			return who.Requester{}, &targetError{"sender_ambiguous_session", fmt.Sprintf("the caller %q matches more than one session; give more of the id", id)}
		}
		return who.Requester{}, &targetError{"sender_" + code, fmt.Sprintf("the caller %q is not a session on this daemon", id)}
	}
	r, ok := b.RequestFrom(s.ID)
	if !ok {
		return who.Requester{}, &targetError{"sender_session_not_found", fmt.Sprintf("the caller %q has ended", id)}
	}
	p, _ := r.Party()
	if err := b.Check(p); err != nil {
		return who.Requester{}, err
	}
	return r, nil
}
func (d *Daemon) requestFromMessage(source *protocol.SessionID, profile *string, b who.Bindings) (who.Requester, error) {
	if id := protocol.TrimID(protocol.Deref(source)); id != "" {
		r, err := d.requestFromSession(id, b)
		if err != nil {
			return who.Requester{}, err
		}
		if asked := strings.TrimSpace(protocol.Deref(profile)); asked != "" {
			owner, err := d.store.GetProfile(r.ProfileID())
			if err != nil {
				return who.Requester{}, err
			}
			if asked != owner.ID && !strings.EqualFold(asked, owner.Name) {
				return who.Requester{}, fmt.Errorf("session %s belongs to profile %q (%s), not profile %q it sent", id, owner.Name, owner.ID, asked)
			}
		}
		return r, nil
	}
	p, err := d.resolveGardenProfile("", protocol.Deref(profile), "")
	if err != nil {
		return who.Requester{}, err
	}
	return who.RequestFromApp(p.ID), nil
}
func (d *Daemon) resolveMember(r who.Requester, text string) (store.CrewIdentity, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return store.CrewIdentity{}, err
	}
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, "chief") {
		key, err := d.chief(r.ProfileID())
		if err != nil {
			return store.CrewIdentity{}, err
		}
		return d.store.CrewIdentity(key)
	}
	var m store.CrewIdentity
	var found bool
	var err error
	if key, ok := strings.CutPrefix(text, "member:"); ok {
		m, found, err = d.store.CrewKeyed(r.ProfileID(), key)
	} else {
		m, found, err = d.store.CrewNamed(r.ProfileID(), text)
	}
	if err != nil {
		return m, err
	}
	if found {
		return m, nil
	}
	profile, err := d.store.GetProfile(r.ProfileID())
	if err != nil {
		return m, err
	}
	hint := ""
	if keyShapedInput.MatchString(text) {
		hint = "; use member:<key> to address a permanent key"
	}
	return m, fmt.Errorf("no crew member named %q in profile %q; use `attn crew list` to see names%s", text, profile.Name, hint)
}
func (d *Daemon) memberName(key who.MemberKey) string {
	m, err := d.store.CrewIdentity(key)
	if err != nil {
		d.logf("crew: read name of %s: %v", key, err)
		return key.String()
	}
	return m.Name
}
func (d *Daemon) storedMemberName(text string) string {
	key, err := who.ParseMemberKey(text)
	if err != nil {
		return text
	}
	return d.memberName(key)
}

func (d *Daemon) claimantFor(r who.Requester, assignee string) (who.Party, error) {
	var party who.Party
	if assignee != "" {
		member, err := d.resolveMember(r, assignee)
		if err != nil {
			return who.Party{}, err
		}
		party = who.Member(member.Key)
	} else if self, ok := r.Party(); ok {
		party = self
	} else {
		return who.Party{}, errors.New("tending records who claims the seed, and the user claims nothing; name the crew member: attn seed tend <seed> --for <name>")
	}
	return party, d.checkGardenClaimant(party)
}

func (d *Daemon) checkGardenClaimant(party who.Party) error {
	if key, member := party.Member(); member {
		identity, err := d.store.CrewIdentity(key)
		if err != nil {
			return err
		}
		if identity.Retired {
			return fmt.Errorf("%s is retired; restore them with attn crew restore %s before assigning work", identity.Name, identity.Name)
		}
	}
	return nil
}

func (d *Daemon) chiefParty(profileID string) (who.Party, error) {
	chief, err := d.chief(profileID)
	if err != nil {
		return who.Party{}, err
	}
	return who.Member(chief), nil
}
func (d *Daemon) gardenRequester(source *protocol.SessionID, profile *string) (who.Requester, error) {
	b, err := d.bindings()
	if err != nil {
		return who.Requester{}, err
	}
	return d.requestFromMessage(source, profile, b)
}
func (d *Daemon) seedMoveError(err error, b who.Bindings) error {
	var refused *garden.TakeoverRefused
	if !errors.As(err, &refused) {
		return err
	}
	return fmt.Errorf("%s is being tended by %s, and attn seed %s takes it from them. Pass --force to act anyway; the log will record it. Or leave a note: attn seed note %s -m …", refused.SeedID, d.partyView(refused.Tender, b).Name, refused.Verb, refused.SeedID)
}

type targetError struct{ code, message string }

func (e *targetError) Error() string { return e.message }
func targetNotFound(text string) error {
	return &targetError{"session_or_crew_member_not_found", fmt.Sprintf("no session, crew member or seed matches %q; attn agent list names sessions and attn crew list names members", text)}
}
func (d *Daemon) resolveAddress(r who.Requester, b who.Bindings, text string) (who.Address, error) {
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, "chief") {
		if err := d.requireHome(crew.Surface); err != nil {
			return who.Address{}, err
		}
		key, err := d.chief(r.ProfileID())
		return who.Member(key).Address(), err
	}
	if key, ok := strings.CutPrefix(text, "member:"); ok {
		if err := d.requireHome(crew.Surface); err != nil {
			return who.Address{}, err
		}
		m, found, err := d.store.CrewKeyed(r.ProfileID(), key)
		if err != nil {
			return who.Address{}, err
		}
		if !found {
			return who.Address{}, targetNotFound(text)
		}
		return who.Member(m.Key).Address(), nil
	}
	if raw, ok := strings.CutPrefix(text, "session:"); ok {
		a, err := who.ParseAddress(text)
		if err != nil {
			return who.Address{}, targetNotFound(text)
		}
		profile, err := d.store.GardenSessionProfileID(protocol.SessionID(raw))
		if err != nil {
			return who.Address{}, err
		}
		if d.hubManager != nil {
			if s := d.hubManager.RemoteSession(protocol.SessionID(raw)); s != nil {
				profile = s.ProfileID
			}
		}
		if profile == "" || profile != r.ProfileID() {
			return who.Address{}, targetNotFound(text)
		}
		return a, nil
	}
	seedID, seedRef := strings.CutPrefix(text, "seed:")
	if !seedRef && garden.ValidateID(text) == nil {
		seedID = text
		seedRef = true
	}
	if seedRef {
		if err := d.requireHome(garden.Surface); err != nil {
			return who.Address{}, err
		}
		seed, _, err := d.readSeed(seedID)
		if err != nil || seed.ProfileID != r.ProfileID() {
			return who.Address{}, targetNotFound(text)
		}
		return who.ToTenderOf(seed.ID), nil
	}
	status, err := d.enrollmentStatus()
	if err != nil {
		return who.Address{}, err
	}
	if status.IsHome() {
		m, found, err := d.store.CrewNamed(r.ProfileID(), text)
		if err != nil {
			return who.Address{}, err
		}
		if found {
			return who.Member(m.Key).Address(), nil
		}
	}
	s, code := d.resolveSessionByIDOrPrefix(text, r.ProfileID())
	if s == nil {
		if code == "ambiguous_session" {
			return who.Address{}, &targetError{code, fmt.Sprintf("%q matches more than one session; give more of the id", text)}
		}
		return who.Address{}, targetNotFound(text)
	}
	p, ok := b.PartyOf(s.ID)
	if !ok {
		return who.Address{}, targetNotFound(text)
	}
	return p.Address(), nil
}
func (d *Daemon) resolveSession(r who.Requester, b who.Bindings, text string) (*protocol.Session, error) {
	a, err := d.resolveAddress(r, b, text)
	if err != nil {
		return nil, err
	}
	onSession := func(id protocol.SessionID) (*protocol.Session, error) {
		if err := b.CheckSession(id); err != nil {
			return nil, err
		}
		if s := d.store.Get(id); s != nil {
			return s, nil
		}
		if d.hubManager != nil {
			if s := d.hubManager.RemoteSession(id); s != nil {
				return s, nil
			}
		}
		return nil, &targetError{"session_ended", fmt.Sprintf("session %s has ended", shortSessionID(id))}
	}
	onMember := func(k who.MemberKey) (*protocol.Session, error) {
		if err := b.Check(who.Member(k)); err != nil {
			return nil, err
		}
		if id, ok := b.SessionOf(who.Member(k)); ok {
			return onSession(id)
		}
		name := d.memberName(k)
		return nil, &targetError{"crew_member_asleep", fmt.Sprintf("%s is asleep; message them: attn agent msg %s", name, name)}
	}
	return who.SwitchAddress(a, onSession, onMember, func(seedID string) (*protocol.Session, error) {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		p, ok := seed.Claim.Lasts(b)
		if !ok {
			return nil, &targetError{"seed_untended", fmt.Sprintf("nobody is tending %s; leave a note: attn seed note %s -m …", seedID, seedID)}
		}
		return who.SwitchParty(p, onSession, onMember)
	})
}
func (d *Daemon) mailboxesOf(id protocol.SessionID, b who.Bindings) ([]who.Address, error) {
	if err := b.CheckSession(id); err != nil {
		return nil, err
	}
	a := b.AddressesOf(id)
	status, err := d.enrollmentStatus()
	if err != nil {
		return nil, err
	}
	if !status.IsHome() {
		return a, nil
	}
	pending, err := d.store.PendingSeedInboxAddresses()
	if err != nil {
		return nil, err
	}
	for _, address := range pending {
		to, err := (delivery{d, b}).recipientOf(address)
		if err != nil {
			var unavailable *who.UnreadableMemberError
			if errors.As(err, &unavailable) {
				continue
			}
			return nil, err
		}
		if to.ring != nil && to.ring.ID == id {
			a = append(a, address)
		}
	}
	return a, nil
}
func (d *Daemon) partyView(p who.Party, b who.Bindings) protocol.PartyView {
	v := protocol.PartyView{Ref: p.Ref(), Name: p.String()}
	if k, ok := p.Member(); ok {
		v.Name = d.memberName(k)
	} else if id, ok := p.Session(); ok {
		v.Name = d.sessionPartyName(id)
	}
	if id, ok := b.SessionOf(p); ok {
		v.SessionID = protocol.Ptr(id)
	}
	return v
}
func (d *Daemon) sessionPartyName(id protocol.SessionID) string {
	if session := d.store.Get(id); session != nil {
		return sessionDisplayName(session)
	}
	if d.hubManager != nil {
		if session := d.hubManager.RemoteSession(id); session != nil {
			return sessionDisplayName(session)
		}
	}
	if entry := d.store.SessionLedgerEntry(id); entry != nil && strings.TrimSpace(entry.Label) != "" {
		return entry.Label
	}
	return shortSessionID(id)
}
func (d *Daemon) actorView(a who.Actor) protocol.ActorView {
	v := protocol.ActorView{Ref: a.Ref(), Name: a.String()}
	if a == who.User() {
		v.Name = "the user"
	} else if p, ok := a.Party(); ok {
		if k, ok := p.Member(); ok {
			v.Name = d.memberName(k)
		} else if id, ok := p.Session(); ok {
			v.Name = d.sessionPartyName(id)
		}
	}
	return v
}
func (d *Daemon) replyTo(p who.Party) string {
	if k, ok := p.Member(); ok {
		return d.memberName(k)
	}
	return p.String()
}
func (d *Daemon) addressName(a who.Address, b who.Bindings) string {
	name, err := who.SwitchAddress(a, func(id protocol.SessionID) (string, error) {
		if s := d.store.Get(id); s != nil {
			return sessionDisplayName(s), nil
		}
		return shortSessionID(id), nil
	}, func(k who.MemberKey) (string, error) { return d.memberName(k), nil }, func(id string) (string, error) { return id, nil })
	if err != nil {
		return a.String()
	}
	return name
}
func (d *Daemon) crewFactAddress(ev bus.Event) (who.Address, error) {
	k, err := who.ParseMemberKey(ev.Subject)
	if err != nil {
		return who.Address{}, err
	}
	return who.Member(k).Address(), nil
}
func (d *Daemon) actorOfRef(ref protocol.ActorRef) (who.Actor, error) {
	if ref == "" {
		return who.User(), nil
	}
	return who.ParseActor(string(ref))
}
func (d *Daemon) replyTargetError(conn net.Conn, err error) {
	var target *targetError
	if errors.As(err, &target) {
		d.replyAgentMsgError(conn, target.code, target.message)
	} else {
		d.sendError(conn, err.Error())
	}
}
func seedAddressID(a who.Address) string {
	id, _ := who.SwitchAddress(a, func(protocol.SessionID) (string, error) { return "", nil }, func(who.MemberKey) (string, error) { return "", nil }, func(id string) (string, error) { return id, nil })
	return id
}
func (d *Daemon) decorateLedgerActor(entry *protocol.SessionLedgerEntry) {
	if entry.ClosedBy == nil {
		return
	}
	a, err := d.actorOfRef(entry.ClosedBy.Ref)
	if err != nil {
		d.logf("ledger actor: %v", err)
		return
	}
	entry.ClosedBy = protocol.Ptr(d.actorView(a))
}

func (d *Daemon) unregisterSessionClose(msg *protocol.UnregisterMessage) (store.SessionClose, error) {
	by, err := d.actorOfRef(protocol.Deref(msg.ClosedBy))
	if err != nil {
		return store.SessionClose{}, err
	}
	return store.SessionClose{By: by, Reason: strings.TrimSpace(protocol.Deref(msg.CloseReason))}, nil
}

func (d *Daemon) mailRefusal(to who.Address) error {
	_, err := who.SwitchAddress(to,
		func(protocol.SessionID) (struct{}, error) { return struct{}{}, nil },
		func(key who.MemberKey) (struct{}, error) {
			member, err := d.store.CrewIdentity(key)
			if err != nil {
				return struct{}{}, err
			}
			if member.Retired {
				return struct{}{}, fmt.Errorf("%s is retired", member.Name)
			}
			return struct{}{}, nil
		},
		func(string) (struct{}, error) { return struct{}{}, nil })
	return err
}

func (d *Daemon) sessionExists(sessionID protocol.SessionID) bool {
	if d.store != nil && d.store.Get(sessionID) != nil {
		return true
	}
	if d.store != nil && d.store.DelegationSessionReserved(sessionID) {
		return true
	}
	return d.hubManager != nil && d.hubManager.RemoteSession(sessionID) != nil
}
