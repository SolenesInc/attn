package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestGardenAdviceInterruptedByACrashResumesWithTheReviewsFrozenRecipe(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_HEADLESS_TASKS=on")
	s.StartCrashingAt("garden-advice-received")
	app, cli := s.App(), s.Client()
	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	register(t, s, "gardener", "gardener")
	planted, err := cli.SeedPlant("gardener", "Finish checkout", "The checkout needs a discount field.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedTransition("gardener", planted.Seed.ID, "tend", "", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := cli.Unregister("gardener"); err != nil {
		t.Fatal(err)
	}
	started, err := cli.SeedReviewStart()
	if err != nil || started.Review == nil || len(started.Review.Items) != 1 {
		t.Fatalf("start review = %+v, %v; want one abandoned seed", started, err)
	}
	setSetting(t, app, "garden.advisor", `{"agent":"codex","model":"later","effort":"low"}`)
	answer := func(task *fakeagent.HeadlessTask) {
		t.Helper()
		if task.Harness != fakeagent.Claude || task.Model != "sonnet" || task.Effort != "medium" {
			t.Errorf("advice uses %s %q at %q; want the frozen Claude sonnet at medium", task.Harness, task.Model, task.Effort)
		}
		task.Answer(`{"recommendation":"keep_growing","explanation":"Checkout work remains.","evidence":["The discount field is unfinished."]}`)
	}
	answer(s.HeadlessTask())
	s.AwaitCrash()
	s.Start()
	app = s.App()
	answer(s.HeadlessTask())
	ready := testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		return m.Review.Run.ID == started.Review.Run.ID && len(m.Review.Items) == 1 && m.Review.Items[0].Status == "ready"
	}).Review.Items[0]
	if ready.SeedID != planted.Seed.ID || protocol.Deref(ready.Recommendation) != "keep_growing" || protocol.Deref(ready.Error) != "" {
		t.Errorf("resumed advice = %+v; want the original review's genuine advice", ready)
	}
}
