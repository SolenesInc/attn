package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const cacheAskWithDefault = `:::question
Which store should keep the cache?
- [ ] Postgres — already deployed
- [ ] Redis — one more service
Default: Postgres after 2h
:::
`

func TestAnUnansweredDefaultTellsTheAgentOnceAtItsTimeAndTheAskStaysOpen(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "asks_enabled", "true")
		asker := w.bubbleClaude(t, app, "shop")
		openedAt := time.Now()
		asked, err := cli.AskOpen(protocol.SessionID(asker.id), "", cacheAskWithDefault, "", w.Path("shop"))
		if err != nil {
			t.Fatalf("ask: %v", err)
		}
		wantDue := openedAt.Add(2 * time.Hour).UTC().Format(time.RFC3339)
		if q := asked.Parts[0].Question; q == nil || q.Default == nil || q.Default.Choice != "Postgres" || q.Default.DueAt != wantDue {
			t.Fatalf("the ask's question reads %+v; want the default Postgres due at %s", asked.Parts[0].Question, wantDue)
		}
		asker.reply("Asked about the store. <!-- attn:state=idle -->")
		const notice = "go with your default for \"Which store should keep the cache?\": Postgres"

		w.restart()
		cli = w.Client()
		asker.cli = cli
		app = w.App()
		w.advance(2*time.Hour - time.Minute)
		if items := readInbox(t, cli, asker.id, 0).Items; len(items) != 0 || asker.promptsContaining(inboxDoorbell) != 0 {
			t.Fatalf("before the default's time the inbox holds %q and the agent was typed %q; want nothing", inboxContents(items), asker.prompts())
		}
		w.advance(2 * time.Minute)
		if got := asker.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("past the default's time the agent was rung %d times: %q; want once", got, asker.prompts())
		}
		items := readInbox(t, cli, asker.id, 0).Items
		if len(items) != 1 || protocol.Deref(items[0].SourceID) != "ask-default" || !strings.Contains(items[0].Content, notice) || !strings.Contains(items[0].Content, asked.ID) {
			t.Fatalf("past the default's time the inbox holds %+v; want one notice naming the default Postgres of %s", items, asked.ID)
		}
		asker.reply("Going with Postgres. <!-- attn:state=idle -->")
		w.advance(3 * time.Hour)
		if items := readInbox(t, cli, asker.id, 0).Items; len(items) != 0 || asker.promptsContaining(inboxDoorbell) != 1 {
			t.Fatalf("hours after the agent read the notice its inbox holds %q and it was typed %q; want no second notice", inboxContents(items), asker.prompts())
		}
		listed, err := cli.AskList(protocol.SessionID(asker.id))
		if err != nil || len(listed.Asks) != 1 || listed.Asks[0].ID != asked.ID {
			t.Fatalf("after the default fell due the agent lists %+v (%v); want the ask still open", listed, err)
		}

		answerCacheAsk(t, app, asked.ID)
		w.advance(time.Minute)
		if got := asker.promptsContaining("Answer: Postgres"); got != 1 {
			t.Fatalf("the later answer was typed %d times: %q; want once", got, asker.prompts())
		}
	})
}
