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

func (d *Daemon) furnishCrewMember(id store.CrewIdentity, b crewBirth) error {
	home := filepath.Join(d.dataRoot, crew.HomesDirName, id.ProfileID, id.Key.String())
	member := crew.Member{Key: id.Key, HomeDir: home, CharterPath: filepath.Join(home, crew.CharterFileName), AwarenessDirs: []string{}}
	if err := d.applyCrewSettings(&member, &protocol.CrewSetMessage{Agent: protocol.Ptr(b.Agent), Model: protocol.Ptr(b.Model), Effort: protocol.Ptr(b.Effort), Cwd: protocol.Ptr(b.CWD)}); err != nil {
		return err
	}
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
	if err := os.MkdirAll(home, 0o755); err != nil {
		return crewHomeWriteError(id, home, err)
	}
	if err := os.WriteFile(member.CharterPath, []byte(b.Charter), 0o644); err != nil {
		return crewHomeWriteError(id, home, err)
	}
	return nil
}

func crewHomeWriteError(id store.CrewIdentity, home string, err error) error {
	return fmt.Errorf("%s exists, but its home at %s could not be written (%v); `attn crew wake %s` creates it", id.Name, home, err, id.Name)
}

func (d *Daemon) handleCrewCreate(conn net.Conn, msg *protocol.CrewCreateMessage) {
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "create", err)
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID)
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
