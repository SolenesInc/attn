package daemon

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

var keyShapedInput = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

type requester struct {
	profileID string
	asking    protocol.SessionID
}

func requestFromApp(profileID string) requester { return requester{profileID: profileID} }
func (r requester) ProfileID() string           { return r.profileID }
func (d *Daemon) requestFromSession(id protocol.SessionID) (requester, error) {
	profile, err := d.callerProfile(id)
	if err != nil {
		return requester{}, err
	}
	return requester{profileID: profile.ID, asking: id}, nil
}
func (d *Daemon) requestFromMessage(source *protocol.SessionID, profile *string) (requester, error) {
	if id := protocol.TrimID(protocol.Deref(source)); id != "" {
		r, err := d.requestFromSession(id)
		if err != nil {
			return requester{}, err
		}
		if asked := strings.TrimSpace(protocol.Deref(profile)); asked != "" {
			owner, err := d.store.GetProfile(r.ProfileID())
			if err != nil {
				return requester{}, err
			}
			if asked != owner.ID && !strings.EqualFold(asked, owner.Name) {
				return requester{}, fmt.Errorf("session %s belongs to profile %q (%s), not profile %q it sent", id, owner.Name, owner.ID, asked)
			}
		}
		return r, nil
	}
	p, err := d.resolveGardenProfile("", protocol.Deref(profile), "")
	if err != nil {
		return requester{}, err
	}
	return requestFromApp(p.ID), nil
}
func (d *Daemon) resolveMember(r requester, text string) (store.CrewIdentity, error) {
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
		hint = "; pass member:<key> for a key"
	}
	return m, fmt.Errorf("no crew member named %q in profile %q; attn crew list names the roster%s", text, profile.Name, hint)
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

func (d *Daemon) tenderName(t garden.Tender) string {
	if t.Member != "" {
		return d.storedMemberName(t.Member)
	}
	return string(t.Session)
}
