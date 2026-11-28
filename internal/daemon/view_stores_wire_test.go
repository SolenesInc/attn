package daemon_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const todoView = `# To do

:::data store="items"
text: text
done: yes-no
:::

:::entry add text
Add an item
:::

:::list items
lead: {done}
title: {text}
press: toggle done
controls: remove
:::
`

type todoItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

func storeItems(t *testing.T, values any) []todoItem {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var items []todoItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("store values %s: %v", raw, err)
	}
	return items
}

func tileItems(t *testing.T, view *protocol.ViewDocument) ([]todoItem, bool) {
	t.Helper()
	for _, values := range view.Values {
		if values.Source == "items" {
			return storeItems(t, values.Values), true
		}
	}
	return nil, false
}

func awaitTodoTile(t *testing.T, app *testworld.Peer, tileID string, from int, want func([]todoItem) bool) protocol.DesktopTileContentMessage {
	t.Helper()
	return testworld.AwaitSince(app, from, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
		if e.TileID != tileID || e.View == nil {
			return false
		}
		items, shown := tileItems(t, e.View)
		return shown && want(items)
	})
}

func texts(items []todoItem) string {
	var out []string
	for _, item := range items {
		mark := " "
		if item.Done {
			mark = "x"
		}
		out = append(out, "["+mark+"] "+item.Text)
	}
	return strings.Join(out, ", ")
}

func partLine(t *testing.T, view *protocol.ViewDocument, is func(protocol.ViewPart) bool) int {
	t.Helper()
	i := slices.IndexFunc(view.Parts, is)
	if i < 0 {
		t.Fatalf("the view has no such part: %+v", view.Parts)
	}
	return view.Parts[i].Line
}

func TestAKeptToDoViewKeepsItemsChangedFromItsTileAndByAnAgentAcrossARestart(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "views_enabled", "true")
	run := func(name string, args, out any) error {
		t.Helper()
		if out == nil {
			out = &json.RawMessage{}
		}
		return w.Client().RunCommand(name, "s1", args, out)
	}
	must := func(name string, args, out any) {
		t.Helper()
		if err := run(name, args, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	folder := w.Path("todo")
	writeViewFolder(t, folder, map[string]string{"view.md": todoView})

	problems, err := cli.OpenMarkdown(filepath.Join(folder, "view.md"), "s1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !slices.ContainsFunc(problems, func(p protocol.ReviewProblem) bool {
		return strings.Contains(p.Message, "only a kept view has a store")
	}) {
		t.Fatalf("opening the to-do view before keeping it answered problems %+v, want one saying to keep it first", problems)
	}

	must("view keep", command.ViewKeepArgs{Path: folder, Name: "todo"}, nil)
	var shown protocol.ViewShowResult
	must("view show", command.ViewShowArgs{View: "todo", Desktop: currentDesktop(t, w).ID}, &shown)
	tile := awaitTodoTile(t, app, shown.TileID, 0, func(items []todoItem) bool { return len(items) == 0 })
	entry := partLine(t, tile.View, func(p protocol.ViewPart) bool { return p.Entry != nil })
	list := partLine(t, tile.View, func(p protocol.ViewPart) bool { return p.List != nil })
	fromTile := func(run protocol.ViewRunMessage) {
		t.Helper()
		run.DesktopID, run.TileID = tile.DesktopID, tile.TileID
		if ran := requestViewRun(app, run); !ran.Success {
			t.Fatalf("running %s %v from the tile: %s", run.Command, run.Args, protocol.Deref(ran.Error))
		}
	}

	mark := len(app.Log())
	fromTile(protocol.ViewRunMessage{Command: "add", Args: []string{"text"}, Part: entry, Text: protocol.Ptr("buy milk")})
	added := awaitTodoTile(t, app, tile.TileID, mark, func(items []todoItem) bool { return texts(items) == "[ ] buy milk" })
	milk, _ := tileItems(t, added.View)

	mark = len(app.Log())
	var fromAgent protocol.ViewDataAddResult
	must("view data add", command.ViewDataAddArgs{View: "todo", Set: "items", Item: json.RawMessage(`{"text": "review oven #91"}`)}, &fromAgent)
	awaitTodoTile(t, app, tile.TileID, mark, func(items []todoItem) bool { return texts(items) == "[ ] buy milk, [ ] review oven #91" })
	oven := storeItems(t, []any{fromAgent.Item})[0]

	mark = len(app.Log())
	fromTile(protocol.ViewRunMessage{Command: "toggle", Args: []string{"done"}, Part: list, Rows: []string{milk[0].ID}})
	awaitTodoTile(t, app, tile.TileID, mark, func(items []todoItem) bool { return texts(items) == "[x] buy milk, [ ] review oven #91" })

	mark = len(app.Log())
	fromTile(protocol.ViewRunMessage{Command: "remove", Part: list, Rows: []string{oven.ID}})
	awaitTodoTile(t, app, tile.TileID, mark, func(items []todoItem) bool { return texts(items) == "[x] buy milk" })

	mark = len(app.Log())
	must("view data set", command.ViewDataSetArgs{View: "todo", Set: "items", ID: milk[0].ID, Item: json.RawMessage(`{"text": "buy oat milk"}`)}, nil)
	awaitTodoTile(t, app, tile.TileID, mark, func(items []todoItem) bool { return texts(items) == "[x] buy oat milk" })
	must("view data add", command.ViewDataAddArgs{View: "todo", Set: "items", Item: json.RawMessage(`{"text": "call the plumber"}`)}, nil)
	var plumber protocol.ViewDataListResult
	must("view data list", command.ViewDataListArgs{View: "todo", Set: "items"}, &plumber)
	must("view data remove", command.ViewDataRemoveArgs{View: "todo", Set: "items", ID: storeItems(t, plumber.Items)[1].ID}, nil)

	for item, want := range map[string]string{
		`{"colour": "red"}`: "no field \"colour\"",
		`{"done": "yes"}`:   "done holds true or false",
	} {
		if err := run("view data add", command.ViewDataAddArgs{View: "todo", Set: "items", Item: json.RawMessage(item)}, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("adding %s answered %v, want a refusal saying %q", item, err, want)
		}
	}

	w.restart()
	app = w.App()
	placeSessions(t, w, app, w.Client(), "s1")
	var listed protocol.ViewDataListResult
	must("view data list", command.ViewDataListArgs{View: "todo", Set: "items"}, &listed)
	if got := texts(storeItems(t, listed.Items)); got != "[x] buy oat milk" {
		t.Fatalf("after a restart the store lists %q, want the one item left, checked off", got)
	}
	awaitTodoTile(t, app, tile.TileID, 0, func(items []todoItem) bool { return texts(items) == "[x] buy oat milk" })

	edited := w.Path("todo-edited")
	writeViewFolder(t, edited, map[string]string{"view.md": "<!-- attn-view 1 todo@1 -->\n" + strings.Replace(todoView, "done: yes-no\n", "done: yes-no\nnote: text\n", 1)})
	mark = len(app.Log())
	must("view edit", command.ViewEditArgs{View: "todo", From: edited}, nil)
	testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(e protocol.DesktopTileContentMessage) bool {
		if e.TileID != tile.TileID || e.View == nil {
			return false
		}
		for _, values := range e.View.Values {
			if raw, _ := json.Marshal(values.Values); values.Source == "items" && strings.Contains(string(raw), `"note":""`) {
				return true
			}
		}
		return false
	})

	var deleted protocol.ViewDeleteResult
	must("view delete", command.ViewDeleteArgs{View: "todo"}, &deleted)
	if want := []protocol.ViewStoreRemoved{{Set: "items", Items: 1}}; !slices.Equal(deleted.Stores, want) {
		t.Fatalf("view delete reports the stores %+v, want %+v", deleted.Stores, want)
	}
	must("view keep", command.ViewKeepArgs{Path: folder, Name: "todo"}, nil)
	must("view data list", command.ViewDataListArgs{View: "todo", Set: "items"}, &listed)
	if len(listed.Items) != 0 {
		t.Fatalf("a view kept again under a deleted view's name lists %+v, want an empty store", listed.Items)
	}
}
