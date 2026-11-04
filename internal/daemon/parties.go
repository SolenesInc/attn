package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type parties struct {
	sessions map[protocol.SessionID]sessionParty
	members  map[string]memberParty
}

type sessionParty struct {
	holds bool
	open  bool
	name  string
}

type memberParty struct {
	name    string
	session protocol.SessionID
}

type workerLiveness struct {
	showing map[protocol.SessionID]bool
	views   map[protocol.SessionID]bool
}

func claimHolds(c store.SessionClaim, workers workerLiveness) bool {
	if c.Reserved {
		return true
	}
	if !c.Open {
		return false
	}
	if !c.LaunchIntent {
		return true
	}
	if c.Host != "" {
		if live, bound := workers.views[c.ID]; bound {
			return live
		}
		return c.RuntimeLive
	}
	return workers.showing[c.ID]
}

func (d *Daemon) readParties() (parties, error) {
	claims, err := d.store.SessionClaims()
	if err != nil {
		return parties{}, err
	}
	workers := workerLiveness{
		showing: d.terminals().showingAny(d.localLiveTerminals(context.Background())),
		views:   d.monitors().liveViews(),
	}
	out := parties{sessions: make(map[protocol.SessionID]sessionParty, len(claims))}
	for _, claim := range claims {
		party := sessionParty{holds: claimHolds(claim, workers), open: claim.Open, name: shortSessionID(claim.ID)}
		if claim.Open {
			party.name = displayName(claim.ID, claim.Label, claim.Title)
		}
		out.sessions[claim.ID] = party
	}
	d.holdStandbyClaims(out.sessions)
	out.members = d.readMemberParties(out.sessions)
	return out, nil
}

func (d *Daemon) holdStandbyClaims(sessions map[protocol.SessionID]sessionParty) {
	rows, err := d.store.SeedsOnStandby()
	if err != nil {
		d.logf("parties: reading seeds on standby: %v", err)
		return
	}
	for seedID, row := range rows {
		id, isSession := row.Tender.Session()
		if !isSession || sessions[id].holds {
			continue
		}
		seed, _, err := d.readSeed(seedID)
		if refusal := protocol.RefusalOf(err); refusal != nil && *refusal == protocol.RefusalNotFound {
			continue
		}
		if err != nil {
			d.logf("parties: reading standby seed %s: %v", seedID, err)
			continue
		}
		if standbyOf(seed, &row) == nil {
			continue
		}
		party, known := sessions[id]
		if !known {
			party.name = shortSessionID(id)
		}
		party.holds = true
		sessions[id] = party
	}
}

func (d *Daemon) readMemberParties(sessions map[protocol.SessionID]sessionParty) map[string]memberParty {
	identities, err := d.store.CrewIdentities()
	if err != nil {
		d.logf("parties: reading crew names: %v", err)
	}
	members := make(map[string]memberParty, len(identities))
	for key, identity := range identities {
		members[key] = memberParty{name: identity.Name}
	}
	roster, _, err := d.readCrewMembersNamed(identities)
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("parties: reading crew bindings: %v", err)
		}
		return members
	}
	for _, member := range roster {
		party, named := members[member.ID]
		if !named {
			party.name = crew.NameFromKey(member.ID)
		}
		if member.BindingSession != "" && sessions[member.BindingSession].holds {
			party.session = member.BindingSession
		}
		members[member.ID] = party
	}
	return members
}

func (d *Daemon) partiesNow() parties {
	snapshot, err := d.readParties()
	if err != nil {
		d.logf("parties: reading claim liveness: %v", err)
	}
	return snapshot
}

func (p parties) holds(party who.Party) bool {
	if id, ok := party.Session(); ok {
		return p.sessions[id].holds
	}
	_, member := party.Member()
	return member
}

func (p parties) view(party who.Party) protocol.PartyView {
	view := protocol.PartyView{Ref: protocol.PartyRef(party.String())}
	if key, ok := party.Member(); ok {
		member, known := p.members[key.String()]
		if !known {
			member.name = crew.NameFromKey(key.String())
		}
		view.Name = member.name
		if member.session != "" {
			view.SessionID = protocol.Ptr(member.session)
		}
		return view
	}
	id, _ := party.Session()
	session, known := p.sessions[id]
	if !known || !session.open {
		view.Name = shortSessionID(id)
		return view
	}
	view.Name = session.name
	if session.holds {
		view.SessionID = protocol.Ptr(id)
	}
	return view
}

func (p parties) actor(a who.Actor) protocol.ActorView {
	if party, ok := a.Party(); ok {
		view := p.view(party)
		return protocol.ActorView{Ref: protocol.ActorRef(view.Ref), Name: view.Name}
	}
	return protocol.ActorView{Ref: protocol.ActorRef(a.String()), Name: actorName(a)}
}

func (r *terminalRegistry) showingAny(live map[harness.TerminalID]struct{}) map[protocol.SessionID]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	showing := make(map[protocol.SessionID]bool)
	for id, entry := range r.byID {
		if _, ok := live[id]; ok {
			showing[entry.shows] = true
		}
	}
	for id, expected := range r.expected {
		if _, ok := live[id]; ok {
			showing[expected.session] = true
		}
	}
	return showing
}

func (r *monitorIndex) liveViews() map[protocol.SessionID]bool {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make(map[protocol.SessionID]bool, len(r.sessions))
	for id, binding := range r.sessions {
		views[id] = binding.applied.Live.V
	}
	return views
}
