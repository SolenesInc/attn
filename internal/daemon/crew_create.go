package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) createCrewMember(profileID, name string) (crew.Member, error) {
	name = strings.TrimSpace(name)
	if err := crew.ValidateName(name); err != nil {
		return crew.Member{}, err
	}
	identities, err := d.store.CrewIdentities()
	if err != nil {
		return crew.Member{}, err
	}
	for _, held := range identities {
		if held.ProfileID != profileID || !strings.EqualFold(held.Name, name) {
			continue
		}
		if held.Retired {
			return crew.Member{}, fmt.Errorf("%s is retired: restore them with `attn crew restore %s`, or pick another name", held.Name, held.Name)
		}
		profile, err := d.store.GetProfile(profileID)
		if err != nil {
			return crew.Member{}, err
		}
		return crew.Member{}, fmt.Errorf("%s already exists in profile %q", held.Name, profile.Name)
	}
	homes := filepath.Join(d.dataRoot, crew.HomesDirName)
	key := crew.UnusedName(strings.ToLower(name), func(key string) bool {
		if _, held := identities[key]; held {
			return true
		}
		_, err := os.Lstat(filepath.Join(homes, key))
		return !errors.Is(err, os.ErrNotExist)
	})
	parent, err := d.notebookCrewHomes(profileID)
	if err != nil {
		return crew.Member{}, err
	}
	home, err := reserveCrewHome(parent, name)
	if err != nil {
		return crew.Member{}, err
	}
	member := crew.Member{ID: key, Name: name, ProfileID: profileID, Home: home, AwarenessDirs: []string{}}
	created := false
	defer func() {
		if !created {
			_ = os.RemoveAll(member.HomeDir(d.dataRoot))
		}
	}()
	if err := os.WriteFile(member.CharterPath(d.dataRoot), nil, 0o644); err != nil {
		return crew.Member{}, err
	}
	if err := d.requireCrewFiles(member); err != nil {
		return crew.Member{}, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, err
	}
	if err := d.store.CreateCrewMember(store.CrewIdentity{Key: key, ProfileID: profileID, Name: name}); err != nil {
		return crew.Member{}, err
	}
	if _, err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
		if undo := d.store.DeleteCrewMember(key); undo != nil {
			d.logf("crew: removing %s's identity after its record failed: %v", key, undo)
		}
		return crew.Member{}, err
	}
	created = true
	d.publishFact(FactCrewRegistered, key, nil)
	d.publishArrangementChanged(profileID)
	d.logf("crew: created %s (key %s) in profile %s at %s", name, key, profileID, member.HomeDir(d.dataRoot))
	return member, nil
}

func (d *Daemon) handleCrewCreate(conn net.Conn, msg *protocol.CrewCreateMessage) {
	profileID, err := d.crewAskerProfile(askerOf(msg.SourceSessionID, msg.ProfileID))
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	created, err := d.createCrewMember(profileID, msg.Name)
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	member, doc, err := d.crewMember(created.ID)
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewCreateResult: &protocol.CrewCreateResult{Member: d.crewMemberWire(member, doc.Rev)}})
}
