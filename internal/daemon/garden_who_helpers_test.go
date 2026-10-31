package daemon_test

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func whoID(ref, kind string) string {
	id, _ := strings.CutPrefix(ref, kind+":")
	if id == ref {
		return ""
	}
	return id
}

func tenderSessionOf(seed protocol.Seed) protocol.SessionID {
	if seed.Tender == nil {
		return ""
	}
	return protocol.SessionID(whoID(string(seed.Tender.Ref), "session"))
}

func tenderMemberOf(seed protocol.Seed) string {
	if seed.Tender == nil {
		return ""
	}
	return whoID(string(seed.Tender.Ref), "member")
}

func planterSessionOf(seed protocol.Seed) protocol.SessionID {
	return protocol.SessionID(whoID(string(seed.Planter.Ref), "session"))
}

func planterMemberOf(seed protocol.Seed) string { return whoID(string(seed.Planter.Ref), "member") }

func authorSessionOf(note protocol.SeedNote) protocol.SessionID {
	return protocol.SessionID(whoID(string(note.Author.Ref), "session"))
}

func authorMemberOf(note protocol.SeedNote) string { return whoID(string(note.Author.Ref), "member") }

func dispatcherMemberOf(session *protocol.Session) string {
	if session == nil || session.Dispatcher == nil {
		return ""
	}
	return whoID(string(session.Dispatcher.Ref), "member")
}
