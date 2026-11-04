package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASeedReminderPostsToTheSeedMailboxOnceAfterARestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		id := w.Spawn(app, fakeagent.Claude, w.Path("tender"))
		tender := w.bootBubbleClaude(t, id)
		tender.reply("Ready. <!-- attn:state=waiting_input -->")
		seed := plantSeedAs(t, cli, id, "Follow the rollout")
		lifeMove(t, cli, id, seed, "tend", "", "")
		w.advance(0)

		set, err := cli.WithGardenProfile("", protocol.SessionID(id)).SeedReminder(seed, client.SeedReminderOptions{In: "20m", Message: "Check the rollout"})
		if err != nil {
			t.Fatal(err)
		}
		if r := set.Seed.Reminder; !set.Changed || r == nil || r.Message != "Check the rollout" {
			t.Fatalf("setting the reminder returned %+v; want the reminder on the seed", set)
		}

		w.restart()
		cli = w.Client()
		tender.cli = cli
		w.advance(19 * time.Minute)
		if items := readInbox(t, cli, id, 0).Items; len(items) != 0 {
			t.Fatalf("before its time the tender's inbox holds %q; want nothing", inboxContents(items))
		}
		w.advance(2 * time.Minute)
		w.advance(time.Hour)
		items := readInbox(t, cli, id, 0).Items
		if len(items) != 1 || items[0].Address != "seed:"+seed || protocol.Deref(items[0].SourceID) != "seed-reminder" ||
			!strings.Contains(items[0].Content, "Check the rollout") || !strings.Contains(items[0].Content, seed) {
			t.Fatalf("past its time the tender's inbox holds %+v; want one reminder in the mailbox of %s and no seed update", items, seed)
		}
		if got := tender.promptsContaining("unread items in your attn inbox"); got != 1 {
			t.Fatalf("the tender was rung %d times: %q; want once", got, tender.prompts())
		}
		if r := lifeShow(t, cli, seed).Seed.Reminder; r != nil {
			t.Fatalf("the seed still carries reminder %+v after it fired", r)
		}
	})
}

func TestAHarvestedSeedsReminderNeverFires(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "tender")
		seed := plantSeedAs(t, cli, "tender", "Follow the rollout")
		lifeMove(t, cli, "tender", seed, "tend", "", "")
		w.advance(0)
		if _, err := cli.WithGardenProfile("", "tender").SeedReminder(seed, client.SeedReminderOptions{In: "20m", Message: "Check the rollout"}); err != nil {
			t.Fatal(err)
		}

		harvested := lifeMove(t, cli, "tender", seed, "harvest", "Rolled out.", "")
		if harvested.Reminder != nil {
			t.Fatalf("the harvested seed carries reminder %+v", harvested.Reminder)
		}
		w.advance(time.Hour)
		lifeMove(t, cli, "tender", seed, "replant", "", "")
		lifeMove(t, cli, "tender", seed, "tend", "", "")
		w.advance(0)
		if items := seedMail(t, cli, "tender", seed); len(items) != 0 {
			t.Fatalf("after the harvest, replanted and tended again, the seed mailbox holds %q; want nothing", inboxContents(items))
		}
	})
}

func seedMail(t *testing.T, cli *client.Client, recipient, seed string) []protocol.AgentInboxItem {
	t.Helper()
	var mail []protocol.AgentInboxItem
	for _, item := range readInbox(t, cli, recipient, 0).Items {
		if item.Address == "seed:"+seed {
			mail = append(mail, item)
		}
	}
	return mail
}
