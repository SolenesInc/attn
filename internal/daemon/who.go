package daemon

import (
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
	status, err := d.enrollmentStatus()
	if err != nil {
		return who.Bindings{}, err
	}
	if status.IsHome() {
		members, _, err := d.readCrewMembers()
		if err != nil {
			return who.Bindings{}, err
		}
		for _, m := range members {
			if m.BindingSession != "" {
				bound[m.Key] = m.BindingSession
			}
		}
	}
	return who.NewBindings(d.sessionFacts, bound), nil
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
		return who.Requester{}, &targetError{"sender_" + code, fmt.Sprintf("the caller %q is not a session on this daemon", id)}
	}
	r, ok := b.RequestFrom(s.ID)
	if !ok {
		return who.Requester{}, &targetError{"sender_session_not_found", fmt.Sprintf("the caller %q has ended", id)}
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

func (d *Daemon) tenderName(profileID string, t garden.Tender) string {
	if t.Member != "" {
		member, found, err := d.store.CrewKeyed(profileID, t.Member)
		if err != nil {
			d.logf("crew: read tender name in %s: %v", profileID, err)
		}
		if found {
			return member.Name
		}
		return t.Member
	}
	return string(t.Session)
}

type targetError struct{ code, message string }

func (e *targetError) Error() string { return e.message }
func targetNotFound(text string) error {
	return &targetError{"session_or_crew_member_not_found", fmt.Sprintf("no session, crew member or seed matches %q; attn agent list names sessions and attn crew list names members", text)}
}
func (d *Daemon) resolveAddress(r who.Requester, b who.Bindings, text string) (who.Address, error) {
	text = strings.TrimSpace(text)
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
	if d.chiefOfProfile(s.ProfileID) == s.ID {
		return who.ToChiefOf(s.ProfileID), nil
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
		p, ok, err := d.seedTender(seed, b)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &targetError{"seed_untended", fmt.Sprintf("nobody is tending %s; leave a note: attn seed note %s -m …", seedID, seedID)}
		}
		return who.SwitchParty(p, onSession, onMember)
	}, func(chief who.ChiefMailbox) (*protocol.Session, error) {
		id := d.chiefOfProfile(chief.ProfileID)
		if id == "" {
			return nil, &targetError{"chief_absent", "this profile has no Chief session"}
		}
		return onSession(id)
	})
}
func (d *Daemon) seedTender(seed garden.Seed, b who.Bindings) (who.Party, bool, error) {
	if seed.TenderMember != "" {
		m, found, err := d.seedTenderMember(seed)
		if err != nil || !found {
			return who.Party{}, false, err
		}
		return who.Member(m.Key), true, nil
	}
	if seed.TenderSession != "" {
		p, lasts := b.PartyOf(seed.TenderSession)
		return p, lasts, nil
	}
	return who.Party{}, false, nil
}
func (d *Daemon) mailboxesOf(id protocol.SessionID, b who.Bindings) ([]who.Address, error) {
	a := b.AddressesOf(id)
	if profile, lasts := d.sessionFacts(id); lasts && d.chiefOfProfile(profile) == id {
		a = append(a, who.ToChiefOf(profile))
	}
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
		v.Name = shortSessionID(id)
		if s := d.store.Get(id); s != nil {
			v.Name = sessionDisplayName(s)
		}
	}
	if id, ok := b.SessionOf(p); ok {
		v.SessionID = protocol.Ptr(id)
	}
	return v
}
func (d *Daemon) actorView(a who.Actor) protocol.ActorView {
	v := protocol.ActorView{Ref: a.Ref(), Name: a.String()}
	if a == who.User() {
		v.Name = "the user"
	} else if p, ok := a.Party(); ok {
		if k, ok := p.Member(); ok {
			v.Name = d.memberName(k)
		} else if id, ok := p.Session(); ok {
			v.Name = shortSessionID(id)
			if s := d.store.Get(id); s != nil {
				v.Name = sessionDisplayName(s)
			}
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
	}, func(k who.MemberKey) (string, error) { return d.memberName(k), nil }, func(id string) (string, error) { return id, nil }, func(who.ChiefMailbox) (string, error) { return "Chief", nil })
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
	id, _ := who.SwitchAddress(a, func(protocol.SessionID) (string, error) { return "", nil }, func(who.MemberKey) (string, error) { return "", nil }, func(id string) (string, error) { return id, nil }, func(who.ChiefMailbox) (string, error) { return "", nil })
	return id
}
func isChiefAddress(a who.Address) bool {
	ok, _ := who.SwitchAddress(a, func(protocol.SessionID) (bool, error) { return false, nil }, func(who.MemberKey) (bool, error) { return false, nil }, func(string) (bool, error) { return false, nil }, func(who.ChiefMailbox) (bool, error) { return true, nil })
	return ok
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
