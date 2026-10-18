package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type crewBirth struct {
	insertIdentity bool
	ProfileID      string
	Name           string
	Charter        string
	Agent          string
	Model          string
	Effort         string
	CWD            string
	Desktop        *store.LaunchDesktopSetting
	Onboarded      bool
}

func (d *Daemon) createCrewMember(b crewBirth) (store.CrewIdentity, error) {
	b.Name = strings.TrimSpace(b.Name)
	if err := crew.ValidateName(b.Name); err != nil {
		return store.CrewIdentity{}, err
	}
	if existing, found, err := d.store.CrewNamed(b.ProfileID, b.Name); err != nil {
		return store.CrewIdentity{}, err
	} else if found {
		if existing.Retired {
			return store.CrewIdentity{}, fmt.Errorf("%s is retired: restore it or pick another name", existing.Name)
		}
		profile, err := d.store.GetProfile(b.ProfileID)
		if err != nil {
			return store.CrewIdentity{}, err
		}
		return store.CrewIdentity{}, fmt.Errorf("%s already exists in %s", existing.Name, profile.Name)
	}
	identity := store.CrewIdentity{Key: who.MintMemberKey(), ProfileID: b.ProfileID, Name: b.Name}
	b.insertIdentity = true
	return identity, d.furnishCrewMember(identity, b)
}

func (d *Daemon) furnishCrewMember(id store.CrewIdentity, b crewBirth) (resultErr error) {
	member := crew.Member{Onboarded: b.Onboarded, Key: id.Key, AwarenessDirs: []string{}}
	if b.insertIdentity {
		if err := d.applyCrewSettings(&member, &protocol.CrewSetMessage{Agent: protocol.Ptr(b.Agent), Model: protocol.Ptr(b.Model), Effort: protocol.Ptr(b.Effort), Cwd: protocol.Ptr(b.CWD)}); err != nil {
			return err
		}
	} else {
		member.Agent = b.Agent
		member.Model = b.Model
		member.Effort = b.Effort
		member.CWD = b.CWD
	}
	if !d.isChief(id.Key) || member.Agent != "" {
		if err := d.furnishCrewHome(id, &member, b.Charter); err != nil {
			return err
		}
	}
	defer func() {
		if resultErr != nil && member.Home != "" {
			_ = os.RemoveAll(member.HomeDir(d.dataRoot))
		}
	}()
	if err := d.validateCrewMemberPaths(member); err != nil {
		return err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return err
	}
	body, err := member.Encode()
	if err != nil {
		return err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, id.Key.String(), false)
	birth := store.NewCrewMember{Identity: id, Doc: store.DocumentWrite{Schema: *schema, ID: id.Key.String(), Body: body, Expected: protocol.Ptr(int64(docstore.ExpectAbsent))}, Fact: fact, Desktop: b.Desktop}
	var written store.DocumentWriteResult
	if b.insertIdentity {
		written, err = d.store.CreateCrewMember(birth, time.Now())
	} else {
		written, err = d.store.FurnishCrewMember(birth, time.Now())
	}
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.publishFact(FactCrewRegistered, id.Key.String(), nil)
	d.publishArrangementChanged(id.ProfileID)
	return nil
}

func writeCrewCharter(path, charter string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".charter-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.WriteString(charter); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (d *Daemon) handleCrewCreate(conn net.Conn, msg *protocol.CrewCreateMessage) {
	bindings, err := d.bindings()
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, bindings)
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	b := crewBirth{ProfileID: r.ProfileID(), Name: msg.Name, Agent: protocol.Deref(msg.Agent), Model: protocol.Deref(msg.Model), Effort: protocol.Deref(msg.Effort), CWD: protocol.Deref(msg.Cwd)}
	if msg.LaunchDesktopName != nil && msg.LaunchDesktop == nil {
		d.sendCrewError(conn, "create", fmt.Errorf("--desktop-name needs --launch-desktop own or an empty slot"))
		return
	}
	if msg.LaunchDesktop != nil {
		desktop, err := d.launchDesktopFromRef(b.ProfileID, b.Name, *msg.LaunchDesktop, msg.LaunchDesktopName)
		if err != nil {
			d.sendCrewError(conn, "create", err)
			return
		}
		b.Desktop = &desktop
	}
	id, err := d.createCrewMember(b)
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	member, doc, err := d.crewMember(id.Key)
	if err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewCreateResult: &protocol.CrewCreateResult{Member: d.crewMemberWire(member, doc.Rev)}})
}
