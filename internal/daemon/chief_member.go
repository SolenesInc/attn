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

type errMemberHasNoHarness struct{ name, alias string }

func (e errMemberHasNoHarness) Error() string { return protocol.CLIText(e.Mentioned()) }

func (e errMemberHasNoHarness) Mentioned() string {
	return fmt.Sprintf("%s has no harness yet; pick one with {attn crew set %s --agent <harness> --model <model>}", e.name, e.alias)
}

func (e errMemberHasNoHarness) Refusal() protocol.Refusal { return protocol.RefusalInvalidInput }

func memberHasNoHarness(member crew.Member) error {
	return errMemberHasNoHarness{name: member.Name, alias: crewAlias(member)}
}

func crewAlias(member crew.Member) string {
	if member.Chief {
		return crew.ChiefAlias
	}
	return member.Name
}

func (d *Daemon) ensureChiefs() {
	if d.store == nil || !d.enrollmentStatus().IsHome() {
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
	} else if err := d.store.StartChiefOnOwnDesktop(key); err != nil {
		return err
	}
	member := crew.Member{ID: key, Name: crew.ChiefName, ProfileID: profileID, AwarenessDirs: []string{}, FirstDay: &crew.FirstDay{Onboarding: protocol.OnboardingPathFull}}
	d.prefillChiefFromLegacySession(&member)
	if member.FirstDay.PreviousChief != "" || d.attnUsedBefore() {
		member.FirstDay.Onboarding = protocol.OnboardingPathTour
	}
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
	d.logf("chief: created %s (key %s) in profile %s at %s, prefilled from session %q, onboarding on the %s path", member.Name, key, profileID, home, member.FirstDay.PreviousChief, member.FirstDay.Onboarding)
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
		member.Model, member.Effort = strings.TrimSpace(intent.Model), intent.Effort
	}
	if member.Model == "" && d.factsOrNone(member.Harness()).CrewModel != "" {
		member.Model = crew.HarnessDefaultModel
	}
	if dir := d.localDirectory(session); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			member.CWD = dir
		}
	}
}

func (d *Daemon) attnUsedBefore() bool {
	ledger, err := d.store.SessionLedger(store.SessionLedgerQuery{Scope: store.SessionLedgerAll, Limit: 1})
	if err != nil {
		d.logf("chief: reading the session ledger to pick the onboarding path: %v", err)
		return true
	}
	return len(ledger.Entries) > 0
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
	member, ok := d.chiefMember(profileID)
	return who.Member(member.Key), ok && !member.Retired
}

func chiefRefusesRetire(member crew.Member) error {
	if !member.Chief {
		return nil
	}
	return protocol.Refuse(protocol.RefusalConflict, "%s is this profile's Chief and can't be retired; its session can close: {attn crew close chief}", member.Name)
}

func crewStartRefusal(member crew.Member, agent string) error {
	if agent == "" && member.Harness() == "" {
		return memberHasNoHarness(member)
	}
	return nil
}

func (d *Daemon) profileDeleteRefusal(profileID string) error {
	chief, ok := d.chiefMember(profileID)
	if ok && d.crewBindingLive(chief) {
		return protocol.Refuse(protocol.RefusalConflict, "%s is in session; close the chief's session first: {attn crew close chief}", chief.Name)
	}
	return nil
}
