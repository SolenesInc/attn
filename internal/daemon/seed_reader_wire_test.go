package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSeedDocumentShowsBodyChildrenLogAndWhetherItsTenderHolds(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	tender := spawnPanes(w, app, w.Path("tender"))[0]
	body := "# Crown\n\nRead this."
	crown, err := cli.SeedPlant(protocol.SessionID(tender.session), "Crown", body, "", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := cli.SeedPlant(protocol.SessionID(tender.session), "Child", "", crown.Seed.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedPlant(protocol.SessionID(tender.session), "Grandchild", "", child.Seed.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedNote(protocol.SessionID(tender.session), crown.Seed.ID, "reader log entry", "", false, nil); err != nil {
		t.Fatal(err)
	}
	lifeMove(t, cli, tender.session, crown.Seed.ID, "tend", "", "")

	live := seedReaderDocument(app, crown.Seed.ID)
	if !live.Success || live.Document == nil {
		t.Fatalf("reading the crown = %+v", live)
	}
	doc := live.Document
	if doc.Seed.Body != body {
		t.Errorf("the document body = %q, want %q", doc.Seed.Body, body)
	}
	if children := lifeSeedIDs(doc.Children); !slices.Equal(children, []string{child.Seed.ID}) {
		t.Errorf("the document children = %v, want only the immediate child %s", children, child.Seed.ID)
	}
	if doc.NotesTotal != 1 || len(doc.Notes) != 1 || doc.Notes[0].Body != "reader log entry" {
		t.Errorf("the document log = %+v of %d, want the one note", doc.Notes, doc.NotesTotal)
	}
	if !doc.Seed.Claimed || string(protocol.Deref(protocol.Deref(doc.Seed.Tender).SessionID)) != tender.session {
		t.Errorf("while %s is live the document reads tender %q holding=%t, want it holding", tender.session, protocol.Deref(protocol.Deref(doc.Seed.Tender).SessionID), doc.Seed.Claimed)
	}

	closePane(app, tender)
	ended := seedReaderDocument(app, crown.Seed.ID)
	if !ended.Success || ended.Document == nil || ended.Document.Seed.Claimed || protocol.Deref(ended.Document.Seed.Tender).Ref != protocol.PartyRef("session:"+tender.session) {
		t.Errorf("after the tender ended the document = %+v, want tender %s kept without a live hold", ended.Document, tender.session)
	}

	if unknown := seedReaderDocument(app, "s-ffffff"); unknown.Success || !strings.Contains(protocol.Deref(unknown.Error), "s-ffffff") {
		t.Errorf("reading an unplanted seed = %+v, want a refusal naming s-ffffff", unknown)
	}
}

func TestOpeningASeedDocksBesideItsCallerOrOnTheCurrentDesktop(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("opener"), w.Path("tender"))
	opener, tender := panes[0], panes[1]

	t.Run("beside the opener, bound to the tender until it lets go", func(t *testing.T) {
		seed := plantSeedAs(t, cli, opener.session, "Read me")
		lifeMove(t, cli, tender.session, seed, "tend", "", "")
		opened := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: seed, SessionID: protocol.Ptr(protocol.SessionID(opener.session))})
		tileID := protocol.Deref(opened.TileID)
		if !opened.Success || protocol.Deref(opened.DesktopID) != opener.desktop || tileID == "" {
			t.Fatalf("opening beside %s = %+v", opener.session, opened)
		}
		seedReaderTileIs(t, w, opener.desktop, tileID, seed, tender.session)

		lifeMove(t, cli, tender.session, seed, "park", "", "")
		reopened := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: seed, SessionID: protocol.Ptr(protocol.SessionID(opener.session))})
		if !reopened.Success || protocol.Deref(reopened.TileID) != tileID {
			t.Fatalf("reopening = %+v, want tile %s again", reopened, tileID)
		}
		seedReaderTileIs(t, w, opener.desktop, tileID, seed, opener.session)
	})

	t.Run("a standalone open lands on the current desktop bound to no agent", func(t *testing.T) {
		focusAgent(t, w, app, opener.session)
		first, second := plantSeedAs(t, cli, "", "Read from Crew"), plantSeedAs(t, cli, "", "Another seed")

		opened := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: first, Standalone: protocol.Ptr(true)})
		desktopID, tileID := protocol.Deref(opened.DesktopID), protocol.Deref(opened.TileID)
		if !opened.Success || desktopID != currentDesktop(t, w).ID {
			t.Fatalf("a standalone open = %+v, want it on the current desktop", opened)
		}
		seedReaderTileIs(t, w, desktopID, tileID, first, "")

		if again := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: first, Standalone: protocol.Ptr(true)}); protocol.Deref(again.TileID) != tileID {
			t.Errorf("reopening = %+v, want tile %s again", again, tileID)
		}
		desktop := desktopOfDelegate(t, w, desktopID)
		if navigated := desktopAction(app, protocol.DesktopUpdateTileMessage{
			Cmd: protocol.CmdDesktopUpdateTile, DesktopID: desktopID, TileID: tileID, TileParams: protocol.Ptr(second), ExpectedRevision: desktop.Revision,
		}); !navigated.Success {
			t.Fatalf("navigating the reader to %s: %s", second, protocol.Deref(navigated.Error))
		}
		if again := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: first, Standalone: protocol.Ptr(true)}); !again.Success {
			t.Errorf("reopening after navigating away = %+v", again)
		}
		seedReaderTileIs(t, w, desktopID, protocol.Deref(opened.TileID), first, "")
	})

	t.Run("an unplanted seed is named", func(t *testing.T) {
		if refused := seedReaderOpen(app, protocol.OpenSeedMessage{SeedID: "s-ffffff", SessionID: protocol.Ptr(protocol.SessionID(opener.session))}); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "s-ffffff") {
			t.Errorf("opening an unplanted seed = %+v, want a refusal naming it", refused)
		}
	})

	t.Run("a markdown and a seed opened at once both keep their tiles", func(t *testing.T) {
		seed := plantSeedAs(t, cli, opener.session, "Concurrent seed")
		file := markdownFile(t, w, "concurrent.md")
		other := w.App()
		markdownRequest, seedRequest := uuid.NewString(), uuid.NewString()
		app.Send(protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: file, SessionID: protocol.Ptr(protocol.SessionID(tender.session)), RequestID: protocol.Ptr(markdownRequest)})
		other.Send(protocol.OpenSeedMessage{Cmd: protocol.CmdOpenSeed, SeedID: seed, SessionID: protocol.Ptr(protocol.SessionID(tender.session)), RequestID: protocol.Ptr(seedRequest)})
		markdown := testworld.Await(app, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool { return protocol.Deref(r.RequestID) == markdownRequest })
		opened := testworld.Await(other, protocol.EventOpenSeedResult, func(r protocol.OpenSeedResultMessage) bool { return protocol.Deref(r.RequestID) == seedRequest })
		if !markdown.Success || !opened.Success {
			t.Fatalf("the simultaneous opens = %+v and %+v", markdown, opened)
		}
		tiles := desktopTree(t, desktopOfDelegate(t, w, tender.desktop)).tiles()
		if _, ok := tiles[protocol.Deref(markdown.TileID)]; !ok {
			t.Errorf("the desktop lost the markdown tile %s: %+v", protocol.Deref(markdown.TileID), tiles)
		}
		if _, ok := tiles[protocol.Deref(opened.TileID)]; !ok {
			t.Errorf("the desktop lost the seed tile %s: %+v", protocol.Deref(opened.TileID), tiles)
		}
	})
}

func TestAnnotationDraftsFollowTheirTypedDocumentSource(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	planted, err := cli.SeedPlant("", "Annotated seed", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seed := planted.Seed.ID
	uri := "attn://seed/" + seed
	saved := testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: uuid.NewString(), DocumentUri: uri, SourceKind: "seed", SeedID: protocol.Ptr(seed),
		Generation: 1, Annotations: []protocol.MarkdownAnnotation{{ID: "g", Type: "global", Text: protocol.Ptr("tighten the scope"), CreatedAt: 1}},
	}, protocol.EventMarkdownAnnotationsSaveResult, func(protocol.MarkdownAnnotationsSaveResultMessage) bool { return true })
	if !saved.Success || protocol.Deref(saved.SeedID) != seed {
		t.Fatalf("saving the seed's draft = %+v", saved)
	}
	got := testworld.Request(app, protocol.MarkdownAnnotationsGetMessage{
		Cmd: protocol.CmdMarkdownAnnotationsGet, RequestID: uuid.NewString(), DocumentUri: uri, SourceKind: "seed", SeedID: protocol.Ptr(seed),
	}, protocol.EventMarkdownAnnotationsGetResult, func(protocol.MarkdownAnnotationsGetResultMessage) bool { return true })
	if got.Generation != 1 || len(got.Annotations) != 1 || protocol.Deref(got.Annotations[0].Text) != "tighten the scope" {
		t.Fatalf("reading %s = %+v, want the saved draft", uri, got)
	}

	submitted := testworld.Request(app, protocol.MarkdownAnnotationsSubmitMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSubmit, RequestID: uuid.NewString(), DocumentUri: uri, SourceKind: "seed",
		SeedID: protocol.Ptr(seed), TargetSeedID: protocol.Ptr(seed),
	}, protocol.EventMarkdownAnnotationsSubmitResult, func(protocol.MarkdownAnnotationsSubmitResultMessage) bool { return true })
	if !submitted.Success {
		t.Fatalf("submitting the seed's annotations = %+v", submitted)
	}
	if notes := lifeNoteBodies(t, cli, seed); len(notes) != 1 || !strings.Contains(notes[0], "Seed: "+seed+" — Annotated seed") {
		t.Errorf("the seed's log = %q, want one note naming the seed", notes)
	}

	for _, source := range []struct {
		uri, path string
		accepted  bool
	}{
		{"attn://seed/s-wrong", "/tmp/a b.md", false},
		{"attn://file/%2Ftmp%2Fa%20b.md", "/tmp/a b.md", true},
		{"attn://file/%2Ftmp%2Fcaf%C3%A9%20!(x).md", "/tmp/café !(x).md", true},
	} {
		result := testworld.Request(app, protocol.MarkdownAnnotationsSaveMessage{
			Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: uuid.NewString(), DocumentUri: source.uri, SourceKind: "file",
			Path: protocol.Ptr(source.path), Generation: 1,
			Annotations: []protocol.MarkdownAnnotation{{ID: "g", Type: "global", Text: protocol.Ptr("note"), CreatedAt: 1}},
		}, protocol.EventMarkdownAnnotationsSaveResult, func(r protocol.MarkdownAnnotationsSaveResultMessage) bool { return r.DocumentUri == source.uri })
		if source.accepted && !result.Success {
			t.Errorf("saving %s as %s = %s, want it accepted", source.uri, source.path, protocol.Deref(result.Error))
		}
		if !source.accepted && (result.Success || !strings.Contains(protocol.Deref(result.Error), "does not match typed file source")) {
			t.Errorf("saving %s as %s = %+v, want the mismatch refused", source.uri, source.path, result)
		}
	}
}

func seedReaderDocument(app *testworld.Peer, seedID string) protocol.SeedDocumentGetResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SeedDocumentGetMessage{Cmd: protocol.CmdSeedDocumentGet, SeedID: seedID, RequestID: requestID},
		protocol.EventSeedDocumentGetResult, func(r protocol.SeedDocumentGetResultMessage) bool { return r.RequestID == requestID })
}

func seedReaderOpen(app *testworld.Peer, msg protocol.OpenSeedMessage) protocol.OpenSeedResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	msg.Cmd, msg.RequestID = protocol.CmdOpenSeed, protocol.Ptr(requestID)
	return testworld.Request(app, msg, protocol.EventOpenSeedResult, func(r protocol.OpenSeedResultMessage) bool {
		return protocol.Deref(r.RequestID) == requestID
	})
}

func seedReaderTileIs(t *testing.T, w *world, desktopID, tileID, seedID, boundTo string) {
	t.Helper()
	tiles := desktopTree(t, desktopOfDelegate(t, w, desktopID)).tiles()
	tile, ok := tiles[tileID]
	if !ok || tile.TileKind != "seed" || tile.TileParams != seedID || tile.TileSessionID != boundTo {
		t.Errorf("desktop %s holds tiles %+v, want seed tile %s showing %s bound to %q", desktopID, tiles, tileID, seedID, boundTo)
	}
	seeds := 0
	for _, tile := range tiles {
		if tile.TileParams == seedID {
			seeds++
		}
	}
	if seeds != 1 {
		t.Errorf("desktop %s shows %s in %d tiles, want one", desktopID, seedID, seeds)
	}
}
