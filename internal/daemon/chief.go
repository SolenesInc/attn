package daemon

import (
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) chief(profileID string) (who.MemberKey, error) {
	return d.store.ProfileChief(profileID)
}

func (d *Daemon) isChief(key who.MemberKey) bool {
	if key.IsZero() || d.store == nil {
		return false
	}
	id, err := d.store.CrewIdentity(key)
	if err != nil {
		return false
	}
	chief, err := d.chief(id.ProfileID)
	return err == nil && chief == key
}

func (d *Daemon) chiefSessions() map[string]protocol.SessionID {
	sessions := map[string]protocol.SessionID{}
	if d.store == nil {
		return sessions
	}
	status, err := d.enrollmentStatus()
	if err != nil {
		d.logf("read chief home: %v", err)
		return sessions
	}
	if !status.IsHome() {
		return sessions
	}
	chiefs, err := d.store.ProfileChiefs()
	if err != nil {
		d.logf("read profile chiefs: %v", err)
		return sessions
	}
	for profile, key := range chiefs {
		member, _, err := d.crewMember(key)
		if err != nil {
			d.logf("read chief %s: %v", key, err)
			continue
		}
		if member.BindingSession != "" {
			sessions[profile] = member.BindingSession
		}
	}
	return sessions
}

func (d *Daemon) sessionIsChief(id protocol.SessionID) bool {
	if id == "" {
		return false
	}
	for _, session := range d.chiefSessions() {
		if session == id {
			return true
		}
	}
	return false
}

func (d *Daemon) requestedByChief(r who.Requester) bool {
	p, ok := r.Party()
	chief, err := d.chief(r.ProfileID())
	return ok && err == nil && p == who.Member(chief)
}

func (d *Daemon) decorateChief(session *protocol.Session, chiefs map[string]protocol.SessionID) {
	if session == nil {
		return
	}
	session.Chief = nil
	for _, id := range chiefs {
		if id == session.ID {
			session.Chief = protocol.Ptr(true)
			return
		}
	}
}

func (d *Daemon) ensureChiefs() error {
	status, err := d.enrollmentStatus()
	if err != nil {
		return err
	}
	if !status.IsHome() {
		return nil
	}
	upgrades, err := d.store.ChiefUpgrades()
	if err != nil {
		return err
	}
	for _, u := range upgrades {
		if err := d.ensureChief(u.ProfileID, &u); err != nil {
			return err
		}
		if err := d.queueChiefHandover(u); err != nil {
			return err
		}
		if err := d.store.FinishChiefUpgrade(u.Chief); err != nil {
			return err
		}
	}
	profiles, err := d.store.ListProfiles(true)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if p.Chief.IsZero() {
			continue
		}
		if p.Deleted() {
			identity, err := d.store.CrewIdentity(p.Chief)
			if err != nil {
				return err
			}
			if !identity.Retired {
				if _, err := d.retireCrewMember(identity); err != nil {
					return err
				}
			}
			continue
		}
		if err := d.ensureChief(p.ID, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *Daemon) ensureChief(profile string, u *store.ChiefUpgrade) error {
	key, err := d.chief(profile)
	if err != nil {
		return err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return err
	}
	_, found, err := d.store.GetDocument(*schema, key.String())
	if err != nil {
		return err
	}
	if found {
		member, _, err := d.crewMember(key)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(member.HomeDir, 0o755); err != nil {
			return err
		}
		if _, err := os.Stat(member.CharterPath); os.IsNotExist(err) {
			return os.WriteFile(member.CharterPath, []byte(prompts.RenderText("chief", "charter", nil)), 0o644)
		} else {
			return err
		}
	}
	identity, err := d.store.CrewIdentity(key)
	if err != nil {
		return err
	}
	b := crewBirth{ProfileID: profile, Name: crew.ChiefName, Charter: prompts.RenderText("chief", "charter", nil)}
	if u != nil {
		b.Agent = u.Agent
		b.Model = u.Model
		b.Effort = u.Effort
		b.CWD = u.CWD
		b.Onboarded = true
	}
	return d.furnishCrewMember(identity, b)
}

func (d *Daemon) queueChiefHandover(u store.ChiefUpgrade) error {
	tended, watched, err := d.chiefHandoverSeeds(u)
	if err != nil {
		return err
	}
	text := prompts.RenderText("chief", "handover", prompts.Values{"previous_session": string(u.Previous), "tended": tended, "watched": watched})
	_, err = d.sendToInbox(inbox.Item{ID: "chief-handover/" + u.Chief.String(), To: who.Member(u.Chief).Address(), Kind: inbox.Notice, Source: "chief-upgrade", Text: text})
	return err
}

func (d *Daemon) chiefWakeHeld(key who.MemberKey) string {
	if !d.isChief(key) {
		return ""
	}
	member, _, err := d.crewMember(key)
	if err != nil {
		return err.Error()
	}
	if member.Agent == "" {
		return "Chief has no harness yet; mail waits until one is picked: attn crew set chief --agent <harness> --model <model>"
	}
	ever, err := d.store.MemberEverBound(key)
	if err != nil {
		return err.Error()
	}
	if !ever && d.PresenceTier() == PresenceAway {
		d.presenceMu.Lock()
		if d.chiefsAwaitingUser == nil {
			d.chiefsAwaitingUser = make(map[who.MemberKey]struct{})
		}
		d.chiefsAwaitingUser[key] = struct{}{}
		d.presenceMu.Unlock()
		return "Chief starts when you next open attn"
	}
	return ""
}

func (d *Daemon) wakeChiefsAwaitingUser() {
	d.presenceMu.Lock()
	waiting := d.chiefsAwaitingUser
	d.chiefsAwaitingUser = nil
	d.presenceMu.Unlock()
	for key := range waiting {
		d.kickInbox(who.Member(key).Address())
	}
}

func (d *Daemon) nudgeChief(profileID, attemptKey, text string) bool {
	key, err := d.chief(profileID)
	if err != nil {
		d.logf("chief inbox: %v", err)
		return false
	}
	itemID := "chief-inbox/" + strings.TrimSpace(attemptKey)
	if strings.TrimSpace(attemptKey) == "" {
		itemID = "chief-inbox/" + uuid.NewString()
	}
	receipt, err := d.sendToInbox(inbox.Item{ID: itemID, To: who.Member(key).Address(), Kind: inbox.Notice, Source: "notebook-inbox", Text: text})
	if err != nil {
		d.logf("chief inbox: %v", err)
		return false
	}
	return receipt.Rang
}

func (d *Daemon) delegatedFromChiefSessionIDs() map[protocol.SessionID]bool {
	if d.store == nil {
		return nil
	}
	delegated := map[protocol.SessionID]bool{}
	for sessionID := range d.gardenDispatchesFromChief() {
		delegated[sessionID] = true
	}
	return delegated
}

func (d *Daemon) decorateDelegatedFromChief(session *protocol.Session, delegatedFromChief map[protocol.SessionID]bool) {
	if session == nil {
		return
	}
	if delegatedFromChief[session.ID] {
		session.DelegatedFromChief = protocol.Ptr(true)
		return
	}
	session.DelegatedFromChief = nil
}
