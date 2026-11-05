package daemon_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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
		if got := tender.promptsContaining("unread items in your attn inbox"); got != 1 {
			t.Fatalf("the tender was rung %d times: %q; want once", got, tender.prompts())
		}
		items := readInbox(t, cli, id, 0).Items
		if len(items) != 1 || items[0].Address != "seed:"+seed || protocol.Deref(items[0].SourceID) != "seed-reminder" ||
			!strings.Contains(items[0].Content, "Check the rollout") || !strings.Contains(items[0].Content, seed) {
			t.Fatalf("past its time the tender's inbox holds %+v; want one reminder in the mailbox of %s and no seed update", items, seed)
		}
		w.advance(time.Hour)
		if items := readInbox(t, cli, id, 0).Items; len(items) != 0 {
			t.Fatalf("an hour after the tender read the reminder its inbox holds %q; want nothing", inboxContents(items))
		}
		if got := tender.promptsContaining("unread items in your attn inbox"); got != 1 {
			t.Fatalf("the tender was rung %d times after it read the reminder: %q; want once", got, tender.prompts())
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

func TestAnOlderBuildRewritingSeedsLeavesTheirRemindersArmed(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		id := w.Spawn(app, fakeagent.Claude, w.Path("tender"))
		tender := w.bootBubbleClaude(t, id)
		tender.reply("Ready. <!-- attn:state=waiting_input -->")
		arm := func(title string) string {
			seed := plantSeedAs(t, cli, id, title)
			lifeMove(t, cli, id, seed, "tend", "", "")
			if _, err := cli.WithGardenProfile("", protocol.SessionID(id)).SeedReminder(seed, client.SeedReminderOptions{In: "20m", Message: title}); err != nil {
				t.Fatal(err)
			}
			return seed
		}
		parked, untouched, harvested := arm("Parked by the older build"), arm("Untouched"), arm("Harvested by the older build")
		w.advance(0)

		rewriteSeedsAsOlderBuild(t, w, map[string]string{parked: "dormant", harvested: garden.StatusHarvested})
		w.start()
		cli = w.Client()
		tender.cli = cli

		if r := lifeShow(t, cli, parked).Seed.Reminder; r == nil || r.Message != "Parked by the older build" {
			t.Fatalf("the rewritten seed shows reminder %+v; want it still armed", r)
		}
		if r := lifeShow(t, cli, harvested).Seed.Reminder; r != nil {
			t.Fatalf("the seed the older build harvested shows reminder %+v", r)
		}
		w.advance(time.Hour)
		mailboxes := map[string]int{}
		for _, item := range readInbox(t, cli, id, 0).Items {
			mailboxes[item.Address]++
		}
		if want := map[string]int{"seed:" + parked: 1, "seed:" + untouched: 1}; !maps.Equal(mailboxes, want) {
			t.Errorf("the tender's mailboxes hold %v; want %v", mailboxes, want)
		}
		lifeMove(t, cli, id, harvested, "replant", "", "")
		lifeMove(t, cli, id, harvested, "tend", "", "")
		w.advance(time.Hour)
		if items := seedMail(t, cli, id, harvested); len(items) != 0 {
			t.Errorf("after the tick and a replant, the mailbox of %s holds %q; want nothing", harvested, inboxContents(items))
		}
	})
}

func TestAReminderTickThatSawAHarvestLeavesTheReplacementArmed(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "tender")
		seed := plantSeedAs(t, cli, "tender", "Follow the rollout")
		lifeMove(t, cli, "tender", seed, "tend", "", "")
		if _, err := cli.WithGardenProfile("", "tender").SeedReminder(seed, client.SeedReminderOptions{In: "1m", Message: "Old reminder"}); err != nil {
			t.Fatal(err)
		}

		due := w.PauseAt(pausepoint.SeedReminderDue)
		read := w.PauseAt(pausepoint.SeedReminderRead)
		w.advance(2 * time.Minute)
		due.Await()
		lifeMove(t, cli, "tender", seed, "harvest", "Rolled out.", "")
		due.Release()
		read.Await()

		replantAndReplaceReminder(t, cli, "tender", seed)
		read.Release()
		expectOnlyTheReplacementFires(t, w, cli, "tender", seed)
	})
}

func TestAReminderTickThatSawAnOlderBuildsHarvestLeavesTheReplacementArmed(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		id := w.Spawn(app, fakeagent.Claude, w.Path("tender"))
		tender := w.bootBubbleClaude(t, id)
		tender.reply("Ready. <!-- attn:state=waiting_input -->")
		seed := plantSeedAs(t, cli, id, "Follow the rollout")
		lifeMove(t, cli, id, seed, "tend", "", "")
		if _, err := cli.WithGardenProfile("", protocol.SessionID(id)).SeedReminder(seed, client.SeedReminderOptions{In: "1m", Message: "Old reminder"}); err != nil {
			t.Fatal(err)
		}
		w.advance(0)

		rewriteSeedsAsOlderBuild(t, w, map[string]string{seed: garden.StatusHarvested})
		w.start()
		cli = w.Client()
		tender.cli = cli

		read := w.PauseAt(pausepoint.SeedReminderRead)
		w.advance(2 * time.Minute)
		read.Await()

		replantAndReplaceReminder(t, cli, id, seed)
		read.Release()
		expectOnlyTheReplacementFires(t, w, cli, id, seed)
	})
}

func replantAndReplaceReminder(t *testing.T, cli *client.Client, tender, seed string) {
	t.Helper()
	lifeMove(t, cli, tender, seed, "replant", "", "")
	lifeMove(t, cli, tender, seed, "tend", "", "")
	set, err := cli.WithGardenProfile("", protocol.SessionID(tender)).SeedReminder(seed, client.SeedReminderOptions{In: "30m", Message: "New reminder"})
	if err != nil {
		t.Fatal(err)
	}
	if r := set.Seed.Reminder; r == nil || r.Message != "New reminder" {
		t.Fatalf("setting the replacement returned reminder %+v", r)
	}
}

func expectOnlyTheReplacementFires(t *testing.T, w *world, cli *client.Client, tender, seed string) {
	t.Helper()
	synctest.Wait()
	if r := lifeShow(t, cli, seed).Seed.Reminder; r == nil || r.Message != "New reminder" {
		t.Fatalf("after the stale tick the seed shows reminder %+v; want the replacement armed", r)
	}
	w.advance(time.Hour)
	mail := seedMail(t, cli, tender, seed)
	if len(mail) != 1 || !strings.Contains(mail[0].Content, "New reminder") {
		t.Fatalf("the seed mailbox holds %q; want only the replacement", inboxContents(mail))
	}
}

func rewriteSeedsAsOlderBuild(t *testing.T, w *world, statuses map[string]string) {
	t.Helper()
	w.stop()
	older, err := store.NewWithDB(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	schema, found, err := older.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		t.Fatalf("the seeds collection = %v, %v", found, err)
	}
	for seed, status := range statuses {
		doc, _, err := older.GetDocument(*schema, seed)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(doc.Body, &body); err != nil {
			t.Fatal(err)
		}
		delete(body, "reminder")
		delete(body, "remind_at")
		body["status"] = status
		rewritten, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := older.PutDocument(*schema, seed, rewritten, time.Now(), &doc.Rev); err != nil {
			t.Fatal(err)
		}
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}
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
