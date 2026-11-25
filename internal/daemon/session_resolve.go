package daemon

import (
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

var (
	errSessionNotFound  = errors.New(command.CodeSessionNotFound)
	errAmbiguousSession = errors.New(command.CodeAmbiguousSession)
)

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
	closed, err := d.store.ClosedSessionsWithIDPrefix(ref, profileID, 2)
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
		if !strings.HasPrefix(string(session.ID), ref) {
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

func sessionRefusal(ref string, err, notFound error) error {
	switch {
	case errors.Is(err, errSessionNotFound):
		return notFound
	case errors.Is(err, errAmbiguousSession):
		return command.Refuse(command.CodeAmbiguousSession, "%q matches more than one session; give more of the id", protocol.TrimID(ref))
	default:
		return err
	}
}
