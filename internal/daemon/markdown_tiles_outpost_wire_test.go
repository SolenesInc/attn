package daemon_test

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type countedOutpostCalls struct {
	mu      sync.Mutex
	calls   map[string]int
	watched chan []string
}

func (c *countedOutpostCalls) answer(_ *bridge.Conn, incoming ssh.NewChannel) {
	bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
		var call struct {
			Cmd       string   `json:"cmd"`
			Documents []string `json:"documents"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.calls[call.Cmd]++
		c.mu.Unlock()
		switch call.Cmd {
		case protocol.CmdTileDocumentsWatch:
			if c.watched != nil {
				c.watched <- call.Documents
			}
			versions := protocol.TileDocumentVersions{Documents: []protocol.TileDocumentVersion{}}
			for _, path := range call.Documents {
				versions.Documents = append(versions.Documents, protocol.TileDocumentVersion{Path: path, Version: 1})
			}
			return versions, nil
		case protocol.CmdTileDocumentRead:
			return protocol.TileDocument{Content: "# far notes\n", Version: 1}, nil
		}
		return struct{}{}, nil
	})
}

func (c *countedOutpostCalls) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.calls)
}

func TestAnUnchangedOutpostTileCostsTheHomeNoBridgeCalls(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		counted := &countedOutpostCalls{calls: map[string]int{}}
		startFarOutpostWith(t, w, nil, bridge.Handlers{daemon.BridgeCall: counted.answer})
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
		host := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
		}).Outposts[0].ID

		requestID := uuid.NewString()
		opened := testworld.Request(app, protocol.OpenMarkdownMessage{
			Cmd: protocol.CmdOpenMarkdown, Host: protocol.Ptr(host), Path: "/far/notes.md", SessionID: protocol.Ptr(protocol.SessionID("s1")), RequestID: protocol.Ptr(requestID),
		}, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
		if !opened.Success {
			t.Fatalf("opening the outpost's file failed: %s", protocol.Deref(opened.Error))
		}
		testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return protocol.Deref(m.Host) == host && m.Content == "# far notes\n"
		})
		w.advance(time.Second)

		before := counted.snapshot()
		w.advance(60 * time.Second)
		after := counted.snapshot()
		for _, cmd := range []string{protocol.CmdTileDocumentsWatch, protocol.CmdTileDocumentRead, protocol.CmdTileAssetRead} {
			if sent := after[cmd] - before[cmd]; sent != 0 {
				t.Errorf("with the tile shown and its file unchanged for 60s the home sent %d %s calls, want none", sent, cmd)
			}
		}
		t.Logf("bridge calls the home sent the outpost: %v while opening and showing the tile, %v after 60s more", before, after)
	})
}

func TestTheOutpostStopsWatchingWhenTheLastAppLeaves(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		counted := &countedOutpostCalls{calls: map[string]int{}, watched: make(chan []string, 16)}
		startFarOutpostWith(t, w, nil, bridge.Handlers{daemon.BridgeCall: counted.answer})
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
		host := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
		}).Outposts[0].ID
		app.Send(protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Host: protocol.Ptr(host), Path: "/far/notes.md", SessionID: protocol.Ptr(protocol.SessionID("s1"))})
		testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return protocol.Deref(m.Host) == host && m.Content == "# far notes\n"
		})

		for documents := range counted.watched {
			if len(documents) == 1 {
				break
			}
		}
		app.Close()
		w.advance(time.Minute)
		var last []string
		for drained := false; !drained; {
			select {
			case last = <-counted.watched:
			default:
				drained = true
			}
		}
		if len(last) != 0 {
			t.Fatalf("a minute after the last app left, the outpost still watches %v", last)
		}
	})
}

type editableOutpostFile struct {
	mu      sync.Mutex
	content string
	version int
}

func (f *editableOutpostFile) edit(content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.content, f.version = content, f.version+1
}

func (f *editableOutpostFile) answer(_ *bridge.Conn, incoming ssh.NewChannel) {
	bridge.Answer(incoming, func(_ context.Context, raw []byte) (any, error) {
		var call struct {
			Cmd       string   `json:"cmd"`
			Documents []string `json:"documents"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch call.Cmd {
		case protocol.CmdTileDocumentsWatch:
			versions := protocol.TileDocumentVersions{Documents: []protocol.TileDocumentVersion{}}
			for _, path := range call.Documents {
				versions.Documents = append(versions.Documents, protocol.TileDocumentVersion{Path: path, Version: f.version})
			}
			return versions, nil
		case protocol.CmdTileDocumentRead:
			return protocol.TileDocument{Content: f.content, Version: f.version}, nil
		}
		return struct{}{}, nil
	})
}

func TestAnOutpostTileSaysWhenItsOutpostIsUnreachableAndRecoversWithTheCurrentFile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		file := &editableOutpostFile{content: "# before\n", version: 1}
		far := startFarOutpostWith(t, w, nil, bridge.Handlers{daemon.BridgeCall: file.answer})
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "s1")
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
		host := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
		}).Outposts[0].ID
		app.Send(protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Host: protocol.Ptr(host), Path: "/far/notes.md", SessionID: protocol.Ptr(protocol.SessionID("s1"))})
		testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return protocol.Deref(m.Host) == host && m.Content == "# before\n" && m.Error == nil
		})

		mark := app.EventCount()
		far.link.Store(int32(linkRefused))
		far.network.Cut(true)
		w.advance(bridge.LostAfter)
		lost := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return protocol.Deref(m.Host) == host && m.Error != nil
		})
		if !strings.Contains(*lost.Error, "far") || !strings.Contains(*lost.Error, "unreachable") {
			t.Fatalf("the tile's error is %q, want it to name the outpost and say it is unreachable", *lost.Error)
		}

		file.edit("# after the outage\n")
		mark = app.EventCount()
		far.link.Store(int32(linkUp))
		far.network.Cut(false)
		w.advance(2 * time.Second)
		back := testworld.AwaitSince(app, mark, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
			return protocol.Deref(m.Host) == host && m.Error == nil
		})
		if back.Content != "# after the outage\n" {
			t.Fatalf("after the outpost came back the tile shows %q, want the file's current content", back.Content)
		}
	})
}
