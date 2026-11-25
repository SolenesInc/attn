package daemon

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionref"
)

var (
	errSessionNotFound  = errors.New(command.CodeSessionNotFound)
	errAmbiguousSession = errors.New(command.CodeAmbiguousSession)
)

type severalSessionsNamed struct {
	name     string
	sessions []*protocol.Session
}

func (e *severalSessionsNamed) Error() string {
	candidates := make([]string, len(e.sessions))
	for i, session := range e.sessions {
		candidates[i] = fmt.Sprintf("%s %q (%s, %s)", shortSessionID(session.ID), session.Label, session.Agent, session.State)
	}
	return fmt.Sprintf("%q names %d live sessions: %s; pass the id of the one you mean", e.name, len(e.sessions), strings.Join(candidates, ", "))
}

func (e *severalSessionsNamed) Is(target error) bool { return target == errAmbiguousSession }

type sessionMatch struct {
	Live   *protocol.Session
	Closed *protocol.SessionLedgerEntry
}

func (m sessionMatch) ID() protocol.SessionID {
	if m.Live != nil {
		return m.Live.ID
	}
	return m.Closed.ID
}

func (m sessionMatch) ProfileID() string {
	if m.Live != nil {
		return m.Live.ProfileID
	}
	return m.Closed.ProfileID
}

func (d *Daemon) resolveSession(ref, profileID string) (sessionMatch, error) {
	ref = protocol.TrimID(ref)
	if ref == "" {
		return sessionMatch{}, errSessionNotFound
	}
	inScope := func(profile string) bool { return profileID == "" || profile == profileID }
	id := protocol.SessionID(ref)
	if session := d.store.Get(id); session != nil && inScope(session.ProfileID) {
		return sessionMatch{Live: session}, nil
	}
	if entry := d.store.SessionLedgerEntry(id); entry != nil && inScope(entry.ProfileID) {
		return sessionMatch{Closed: entry}, nil
	}
	session, err := d.resolveLiveSession(ref, profileID)
	if err == nil {
		return sessionMatch{Live: session}, nil
	}
	if !errors.Is(err, errSessionNotFound) {
		return sessionMatch{}, err
	}
	closed, err := d.store.ClosedSessionsNamed(ref, profileID, 2)
	switch {
	case err != nil:
		return sessionMatch{}, err
	case len(closed) == 0:
		return sessionMatch{}, errSessionNotFound
	case len(closed) > 1:
		return sessionMatch{}, errAmbiguousSession
	}
	return sessionMatch{Closed: &closed[0]}, nil
}

func (d *Daemon) resolveLiveSession(ref, profileID string) (*protocol.Session, error) {
	ref = protocol.TrimID(ref)
	if ref == "" {
		return nil, errSessionNotFound
	}
	if session := d.store.Get(protocol.SessionID(ref)); session != nil && (profileID == "" || session.ProfileID == profileID) {
		return session, nil
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if profileID != "" && session.ProfileID != profileID {
			continue
		}
		if !sessionref.Names(ref, session.ID) {
			continue
		}
		if match != nil {
			return nil, errAmbiguousSession
		}
		match = session
	}
	if match == nil {
		return nil, errSessionNotFound
	}
	return match, nil
}

func (d *Daemon) resolveTargetSession(ref, profileID string) (sessionMatch, error) {
	match, err := d.resolveSession(ref, profileID)
	if !errors.Is(err, errSessionNotFound) {
		return match, err
	}
	named, err := d.liveSessionNamed(ref, profileID)
	if err != nil {
		return sessionMatch{}, err
	}
	return sessionMatch{Live: named}, nil
}

func (d *Daemon) resolveLiveTargetSession(ref, profileID string) (*protocol.Session, error) {
	session, err := d.resolveLiveSession(ref, profileID)
	if !errors.Is(err, errSessionNotFound) {
		return session, err
	}
	return d.liveSessionNamed(ref, profileID)
}

func (d *Daemon) liveSessionNamed(name, profileID string) (*protocol.Session, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errSessionNotFound
	}
	var named []*protocol.Session
	for _, session := range d.store.List("") {
		if (profileID == "" || session.ProfileID == profileID) && strings.EqualFold(strings.TrimSpace(session.Label), name) {
			named = append(named, session)
		}
	}
	switch len(named) {
	case 0:
		return nil, errSessionNotFound
	case 1:
		return named[0], nil
	}
	slices.SortFunc(named, func(a, b *protocol.Session) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return nil, &severalSessionsNamed{name: name, sessions: named}
}

func ambiguousSessionMessage(ref string, err error, idMessage string) string {
	var several *severalSessionsNamed
	if errors.As(err, &several) {
		return several.Error()
	}
	return fmt.Sprintf(idMessage, protocol.TrimID(ref))
}

func sessionRefusal(ref string, err, notFound error) error {
	switch {
	case errors.Is(err, errSessionNotFound):
		return notFound
	case errors.Is(err, errAmbiguousSession):
		return command.Refuse(command.CodeAmbiguousSession, "%s", ambiguousSessionMessage(ref, err, "%q matches more than one session; give more of the full id"))
	default:
		return err
	}
}
