package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSeedAuthorizationSaysWhereEachChangeWent(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	seed := plant(t, s, "Search endpoint")

	if got := seedAs(t, s, "", "authorization", seed.ID); got != seed.ID+" has no authorization.\n" {
		t.Errorf("reading an unset authorization printed %q", got)
	}
	if got := seedAs(t, s, "", "authorization", seed.ID, "Ask me before pushing."); got != seed.ID+" authorization set. Nobody tends "+seed.ID+"; the next delegation's opening includes it.\n" {
		t.Errorf("setting an untended seed's authorization printed %q", got)
	}
	if got := seedAs(t, s, "", "authorization", seed.ID, "Ask me before pushing."); got != seed.ID+" already has this authorization.\n" {
		t.Errorf("setting the same words again printed %q", got)
	}
	requireLines(t, "the read", seedAs(t, s, "", "authorization", seed.ID), seed.ID+` authorization "Ask me before pushing."  set by the user at `)
	requireLines(t, "show", seedAs(t, s, "", "show", seed.ID), `authorization  "Ask me before pushing."  set by the user at `)

	tender := s.Spawn(s.App(), fakeagent.Claude, s.Path("tender"))
	s.Launched(tender)
	seedAs(t, s, tender, "tend", seed.ID)
	if got := seedAs(t, s, tender, "authorization", seed.ID, "You may push this branch."); got != seed.ID+" authorization set. You tend "+seed.ID+", so nothing is typed.\n" {
		t.Errorf("the tender setting its own seed's authorization printed %q", got)
	}
	if got := seedAs(t, s, "", "authorization", seed.ID, "Ask me before landing."); !strings.HasPrefix(got, seed.ID+" authorization set. ") || !strings.Contains(got, tender[:8]) {
		t.Errorf("the user changing the words while %s tends the seed printed %q; want where the change went", tender, got)
	}
	if got := seedAs(t, s, tender, "authorization", seed.ID, "--clear"); got != seed.ID+" authorization cleared. You tend "+seed.ID+", so nothing is typed.\n" {
		t.Errorf("clearing printed %q", got)
	}
	both := s.Run(testworld.Invocation{Session: tender, Args: []string{"seed", "authorization", seed.ID, "words", "--clear"}})
	if both.Code != 2 || !strings.Contains(both.Stderr, "not both") {
		t.Errorf("words with --clear exited %d: %s", both.Code, both.Stderr)
	}
}
