package main_test

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func stackTenderSession(seed protocol.Seed) protocol.SessionID {
	if seed.Tender == nil {
		return ""
	}
	id, _ := strings.CutPrefix(string(seed.Tender.Ref), "session:")
	return protocol.SessionID(id)
}

func stackAuthorSession(note protocol.SeedNote) protocol.SessionID {
	id, _ := strings.CutPrefix(string(note.Author.Ref), "session:")
	return protocol.SessionID(id)
}
