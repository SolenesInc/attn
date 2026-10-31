package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

var errChiefHasNoHarness = errors.New("the Chief has no harness yet; pick one with `attn crew set chief --agent <harness> --model <model>`")

func (d *Daemon) ensureChiefs() {
	if d.store == nil || !d.crewV2Enabled() || !d.enrollmentStatus().IsHome() {
		return
	}
	chiefs, err := d.store.ProfileChiefMembers()
	if err != nil {
		d.logf("chief: reading the profiles' Chiefs: %v", err)
		return
	}
	for profileID, key := range chiefs {
		if err := d.ensureChief(profileID, key); err != nil {
			d.logf("chief: profile %s has no Chief yet, retrying at the next start: %v", profileID, err)
		}
	}
}

func (d *Daemon) ensureChief(profileID, key string) error {
	if key == "" {
		identities, err := d.store.CrewIdentities()
		if err != nil {
			return err
		}
		key = d.unusedCrewKey(crew.ChiefAlias, identities)
		if err := d.store.CreateChiefMember(profileID, key); err != nil {
			return err
		}
	} else if _, _, err := d.crewMember(key); err == nil {
		return nil
	}
	member := crew.Member{ID: key, Name: crew.ChiefName, ProfileID: profileID, AwarenessDirs: []string{}, FirstDay: &crew.FirstDay{}}
	d.prefillChiefFromLegacySession(&member)
	home, err := d.newCrewHome(profileID, crew.ChiefName)
	if err != nil {
		return err
	}
	member.Home = home
	schema, err := d.crewCollection()
	if err != nil {
		_ = os.RemoveAll(home)
		return err
	}
	if _, err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
		_ = os.RemoveAll(home)
		return err
	}
	d.publishFact(FactCrewRegistered, key, nil)
	d.publishArrangementChanged(profileID)
	d.logf("chief: created %s (key %s) in profile %s at %s, prefilled from session %q", member.Name, key, profileID, home, member.FirstDay.PreviousChief)
	return nil
}

func (d *Daemon) prefillChiefFromLegacySession(member *crew.Member) {
	previous := d.legacyProfileChiefs()[member.ProfileID]
	session := d.store.Get(previous)
	if session == nil {
		return
	}
	member.FirstDay.PreviousChief = previous
	member.Agent = session.Agent
	if intent, ok := d.store.LaunchIntent(previous); ok {
		member.Model, member.Effort = intent.Model, intent.Effort
	}
	if info, err := os.Stat(session.Directory); err == nil && info.IsDir() {
		member.CWD = session.Directory
	}
}

func (d *Daemon) newCrewHome(profileID, name string) (string, error) {
	parent, err := d.notebookCrewHomes(profileID)
	if err != nil {
		return "", err
	}
	home, err := reserveCrewHome(parent, name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(home, crew.CharterFileName), nil, 0o644); err != nil {
		_ = os.RemoveAll(home)
		return "", err
	}
	return home, nil
}

func (d *Daemon) unusedCrewKey(name string, identities map[string]store.CrewIdentity) string {
	homes := filepath.Join(d.dataRoot, crew.HomesDirName)
	return crew.UnusedName(strings.ToLower(name), func(key string) bool {
		if _, held := identities[key]; held {
			return true
		}
		_, err := os.Lstat(filepath.Join(homes, key))
		return !errors.Is(err, os.ErrNotExist)
	})
}

func (d *Daemon) chiefMember(profileID string) (crew.Member, bool) {
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("chief: reading the roster: %v", err)
		}
		return crew.Member{}, false
	}
	for _, member := range members {
		if member.Chief && member.ProfileID == profileID {
			return member, true
		}
	}
	return crew.Member{}, false
}

func (d *Daemon) chiefMemberSessions() map[string]protocol.SessionID {
	sessions := map[string]protocol.SessionID{}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("chief: reading the roster: %v", err)
		}
		return sessions
	}
	for _, member := range members {
		if member.Chief && !member.Retired && d.crewBindingLive(member) {
			sessions[member.ProfileID] = member.BindingSession
		}
	}
	return sessions
}

func (d *Daemon) chiefParty(profileID string) (who.Party, bool) {
	if !d.crewV2Enabled() {
		session := d.legacyProfileChiefs()[profileID]
		return d.partyOfSession(session), session != "" && d.store.Get(session) != nil
	}
	member, ok := d.chiefMember(profileID)
	return who.Member(member.Key), ok && !member.Retired
}

func (d *Daemon) chiefMailbox(profileID string) who.Address {
	if d.crewV2Enabled() {
		if member, ok := d.chiefMember(profileID); ok {
			return who.Member(member.Key).Address()
		}
	}
	return who.ToChief(profileID)
}

func (d *Daemon) chiefRolesChanged() {
	changed := map[protocol.SessionID]bool{}
	for _, session := range d.legacyProfileChiefs() {
		changed[session] = true
	}
	for _, session := range d.chiefMemberSessions() {
		changed[session] = true
	}
	d.pushMonitorPolicies()
	for session := range changed {
		d.publishFact(FactSessionChiefRoleChanged, string(session), nil)
	}
}

func (d *Daemon) crewV2EnabledChanged() {
	d.ensureChiefs()
	d.chiefRolesChanged()
}

func chiefRefusesRetire(member crew.Member) error {
	if !member.Chief {
		return nil
	}
	return fmt.Errorf("%s is this profile's Chief and can't be retired; it can sleep: attn crew sleep chief", member.Name)
}

func (d *Daemon) chiefWakeRefusal(member crew.Member) error {
	if member.Chief && strings.TrimSpace(member.Agent) == "" {
		return errChiefHasNoHarness
	}
	return nil
}

func (d *Daemon) profileDeleteRefusal(profileID string) error {
	chief, ok := d.chiefMember(profileID)
	if ok && d.crewBindingLive(chief) {
		return fmt.Errorf("%s is awake; put the chief to sleep first: attn crew sleep chief", chief.Name)
	}
	return nil
}
