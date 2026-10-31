package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPlainShellAssignsSeedClaimsToMembers(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	var planted protocol.Seed
	s.Attn("seed", "plant", "Assign this seed", "--json").JSON(t, &planted)
	requireStdout(t, s.Attn("seed", "tend", planted.ID, "--for", "Keel"), "tended by Keel")
	requireStdout(t, s.Attn("seed", "show", planted.ID), "tender", "Keel")
}
