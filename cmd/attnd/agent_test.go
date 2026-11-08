package main

import (
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAgentListRowsJoinProfileNamesAndSort(t *testing.T) {
	rows := agentListRows(&client.ListResult{
		Sessions: []protocol.Session{
			{ID: "bbbb2222-1111", Label: "zeta", Agent: "claude", ProfileID: "profile-2", State: "idle"},
			{ID: "aaaa1111-2222", Label: "alpha", Agent: "codex", ProfileID: "profile-1", State: "working", TurnOwed: protocol.Ptr(true)},
		},
		Profiles: []protocol.Profile{
			{ID: "profile-1", Name: "attn"},
			{ID: "profile-2", Name: "notes"},
		},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Profile != "attn" || rows[0].Label != "alpha" || !rows[0].TurnOwed {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].Profile != "notes" || rows[1].TurnOwed {
		t.Fatalf("second row = %+v", rows[1])
	}
}
