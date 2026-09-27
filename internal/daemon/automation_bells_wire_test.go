package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func automationInbox(t *testing.T, r *automationReviewWorld, session string) []string {
	t.Helper()
	var bells []string
	for _, item := range readInbox(t, r.cli, session, 0).Items {
		bells = append(bells, item.Content)
	}
	return bells
}

func automationOneBell(t *testing.T, r *automationReviewWorld, who, session, seed, event, when string) {
	t.Helper()
	if bells := automationInbox(t, r, session); len(bells) != 1 || !strings.Contains(bells[0], seed+" moved: "+event) {
		t.Errorf("%s the %s's inbox holds %q, want one %s bell for %s", when, who, bells, event, seed)
	}
}

func TestAnAutomationThreadRingsOnlyWhenContinuedWorkIsReadyOrWithdrawn(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	r.github.request(42, r.head, false)
	r.refresh()
	first := r.awaitNewRun("review", "delivered")
	reviewer, seed := protocol.Deref(first.SessionID), protocol.Deref(first.SeedID)
	r.w.Launched(reviewer)
	if bells := automationInbox(t, r, reviewer); len(bells) != 0 {
		t.Errorf("the first run rang its own reviewer with %q", bells)
	}

	observer := r.w.Spawn(r.app, fakeagent.Claude, r.w.Path("observer"))
	watcher := r.w.Launched(observer)
	gardenNudgeWatch(t, r.cli, observer, seed, false)
	gardenNudgeMove(t, r.cli, reviewer, seed, "park")
	if prompt := watcher.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the parked seed prompted its watcher with %q, want an inbox doorbell", prompt)
	}
	if _, err := r.cli.SeedShow(observer, seed); err != nil {
		t.Fatal(err)
	}
	if bells := automationInbox(t, r, observer); len(bells) != 0 {
		t.Errorf("after showing the parked seed the observer's inbox holds %q", bells)
	}
	watcher.Reply("Read the seed. <!-- attn:state=idle -->")
	r.rerequest(42)
	continued := r.awaitNewRun("review", "delivered", first)
	if prompt := watcher.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the continued work prompted its watcher with %q, want an inbox doorbell", prompt)
	}
	automationOneBell(t, r, "observer", observer, seed, "work.ready", "after the thread was taken up again")
	automationOneBell(t, r, "reviewer", reviewer, seed, "work.ready", "after the thread was taken up again")
	watcher.Reply("Read the continued work. <!-- attn:state=idle -->")

	upstream := newRepo(t, "upstream")
	later := commitFile(t, upstream, "later.go", "package later\n")
	r.github.withdraw(42)
	r.refresh()
	r.github.request(42, later, false)
	r.refresh()
	held := r.awaitNewRun("review", "pending", first, continued)
	r.github.withdraw(42)
	r.refresh()
	r.awaitRuns("review", func(runs []protocol.AutomationRunSummary) bool {
		return automationRunState(runs, held.ID) == "cancelled"
	})
	if prompt := watcher.Prompted(); !strings.Contains(prompt, inboxDoorbell) {
		t.Fatalf("the withdrawn work prompted its watcher with %q, want an inbox doorbell", prompt)
	}
	automationOneBell(t, r, "observer", observer, seed, "note.added", "after its held continuation was withdrawn")
	automationOneBell(t, r, "reviewer", reviewer, seed, "note.added", "after its held continuation was withdrawn")
	if _, err := r.cli.SeedShow(reviewer, seed); err != nil {
		t.Fatal(err)
	}
	r.refresh()
	r.refresh()
	if bells := automationInbox(t, r, reviewer); len(bells) != 0 {
		t.Errorf("later refreshes rang the reviewer again with %q", bells)
	}
	if notes := automationSeedNotesMentioning(t, r.cli, seed, "(automation run "+held.ID+")"); notes != 1 {
		t.Errorf("the thread noted the withdrawn continuation %d times, want once", notes)
	}
}
