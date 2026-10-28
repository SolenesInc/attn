package daemon_test

import (
	"os"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegatorsHearTheirDelegatesUntilTheyUnwatchAcrossRestarts(t *testing.T) {
	for _, assignment := range []string{"new", "seed", "handover"} {
		t.Run(assignment, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				root := w.bubbleClaude(t, app, "root")
				if err := os.MkdirAll(w.Path("worker"), 0o755); err != nil {
					t.Fatal(err)
				}
				request := delegateFrom(root.id, w.Path("worker"), "Investigate the store", fakeagent.Claude)
				request.RequestID = "watch-proof"
				if assignment != "new" {
					seed := plantDelegationSeed(t, cli, root.id, "existing work")
					request.Assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed)}
					if assignment == "handover" {
						request.Assignment.Handover = &protocol.DelegateHandover{}
					}
				}
				accepted, err := cli.StartDelegation(request)
				if err != nil {
					t.Fatal(err)
				}
				w.advance(0)
				status, statusErr := cli.DelegationStatus(request.RequestID)
				if statusErr != nil {
					t.Fatal(statusErr)
				}
				if status.Error != nil {
					t.Fatal(*status.Error)
				}
				worker := w.bootBubbleClaude(t, string(accepted.SessionID))
				worker.reply("Ready. <!-- attn:state=idle -->")
				delegated, err := cli.Delegate(request)
				if err != nil {
					t.Fatal(err)
				}
				seed := delegated.SeedID
				for _, id := range []string{root.id, worker.id} {
					shown, err := cli.SeedShow(protocol.SessionID(id), seed)
					if err != nil || !shown.Watching || !slices.Contains(shown.WatchingVia, seed) {
						t.Fatalf("%s watch = %+v, %v; want a direct watch on %s", id, shown, err, seed)
					}
				}
				gardenNudgeInboxIsEmpty(t, cli, root.id, "after its own delegation")
				gardenNudgeNote(t, cli, worker.id, seed, "ordinary progress", false)
				w.advance(0)
				gardenNudgeInboxIsEmpty(t, cli, root.id, "after a quiet note")
				gardenNudgeNote(t, cli, worker.id, seed, "ready for review", true)
				w.advance(0)
				if got := root.promptsContaining(inboxDoorbell); got != 1 {
					t.Fatalf("delegator rings = %d, want one: %q", got, root.prompts())
				}
				gardenNudgeOneBell(t, cli, root.id, seed, "note.added")
				root.reply("Read. <!-- attn:state=idle -->")
				gardenNudgeWatch(t, cli, root.id, seed, true)
				gardenNudgeNote(t, cli, worker.id, seed, "after unwatch", true)
				w.advance(0)
				gardenNudgeInboxIsEmpty(t, cli, root.id, "after unwatch")

				w.restart()
				cli = w.Client()
				if _, err := cli.StartDelegation(request); err != nil {
					t.Fatal(err)
				}
				w.advance(0)
				if shown, err := cli.SeedShow(protocol.SessionID(root.id), seed); err != nil || shown.Watching {
					t.Fatalf("after restarting and retrying, watch = %+v, %v; want removed", shown, err)
				}
				gardenNudgeNote(t, cli, worker.id, seed, "after restart", true)
				w.advance(0)
				gardenNudgeInboxIsEmpty(t, cli, root.id, "after restart")
			})
		})
	}
}

func TestACrewDelegatorsWatchStaysWithTheMemberAfterANap(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		_, first := crewSessionInBubble(t, w, nil)
		cli := w.Client()
		if err := os.MkdirAll(w.Path("worker"), 0o755); err != nil {
			t.Fatal(err)
		}
		request := delegateFrom(first.id, w.Path("worker"), "Investigate the store", fakeagent.Claude)
		request.RequestID = "member-watch-proof"
		accepted, err := cli.StartDelegation(request)
		if err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		status, statusErr := cli.DelegationStatus(request.RequestID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.Error != nil {
			t.Fatal(*status.Error)
		}
		worker := w.bootBubbleClaude(t, string(accepted.SessionID))
		worker.reply("Ready. <!-- attn:state=idle -->")
		delegated, err := cli.Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cli.CrewHandoff(protocol.SessionID(first.id), "waiting for the worker", false, protocol.CrewSessionCloseSleep, ""); err != nil {
			t.Fatal(err)
		}
		gardenNudgeNote(t, cli, worker.id, delegated.SeedID, "ready for review", true)
		w.advance(0)
		next := w.bootBubbleClaude(t, string(protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)))
		next.reply("Ready. <!-- attn:state=idle -->")
		items := readInbox(t, cli, next.id, 0).Items
		if len(items) != 1 || items[0].Address != "member:trellis" || protocol.Deref(items[0].SourceID) != delegated.SeedID {
			t.Fatalf("successor inbox = %+v, want the delegate's ring addressed to trellis", items)
		}
		gardenNudgeWatch(t, cli, next.id, delegated.SeedID, true)
		gardenNudgeNote(t, cli, worker.id, delegated.SeedID, "after member unwatch", true)
		w.advance(0)
		gardenNudgeInboxIsEmpty(t, cli, next.id, "after member unwatch")
	})
}
