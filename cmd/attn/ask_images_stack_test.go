package main_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAsksImagesFromAnOutpostShowAtTheHomeAfterTheSourceIsGone(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	turnAsksOn(pair.App)
	asker := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("work"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	terminal := pair.App.Terminal(asker)
	outpost.LaunchedCarrying(terminal)

	shot := writeImage(t, outpost.Path("work", "shots", "before.png"), 2048)
	askFile := outpost.Path("work", "ask.md")
	writeAskFile(t, askFile, "The banner overlaps the tabs:\n\n![before](shots/before.png)\n\n:::question\nShip it?\n- [ ] Yes\n- [ ] No\n:::\n")
	opened := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"ask", "--file", askFile, "--json"}})
	if opened.Code != 0 {
		t.Fatalf("ask from the outpost: %+v", opened)
	}
	var asked protocol.Ask
	opened.JSON(t, &asked)
	removeAll(t, outpost.Path("work"))

	if got := askImage(t, pair.App, asked.ID, outpost.Path("work", "shots", "before.png")); !bytes.Equal(got, shot) {
		t.Fatalf("the home shows %d bytes for the outpost's image, want its %d", len(got), len(shot))
	}
}

func TestAnAsksImagesStayWithItAndOnesOverTheLimitAreRefused(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	turnAsksOn(app)
	asker := s.Spawn(app, fakeagent.Claude, s.Path("work"))
	s.Launched(asker)
	work := s.Path("work")

	beside := writeImage(t, filepath.Join(work, "beside.png"), 1024)
	nested := writeImage(t, filepath.Join(work, "notes", "nested.webp"), 512)
	escaped := writeImage(t, filepath.Join(work, "a(b)&c.png"), 256)
	writeAskFile(t, filepath.Join(work, "notes", "ask.md"), "From the file: ![nested](nested.webp)\n")
	stdinAsk := "Plain ![beside](beside.png), escaped ![escaped](a\\(b\\)&amp;c.png), remote ![remote](https://example.com/r.png), in code `![no](absent.png)`.\n\n" +
		"```\n![no](also-absent.png)\n```\n\n:::question\nWhich?\n<img src=\"beside.png\">\n- [ ] One\n- [ ] Two\n:::\n"
	fromStdin := askAs(t, s, asker, work, stdinAsk)
	fromFile := askAs(t, s, asker, work, "", "--file", filepath.Join(work, "notes", "ask.md"))
	removeAll(t, work)
	if got := askImage(t, app, fromStdin.ID, filepath.Join(work, "beside.png")); !bytes.Equal(got, beside) {
		t.Fatalf("the stdin ask shows %d bytes for beside.png after it was deleted, want its %d", len(got), len(beside))
	}
	if got := askImage(t, app, fromStdin.ID, filepath.Join(work, "a(b)&c.png")); !bytes.Equal(got, escaped) {
		t.Fatalf("the stdin ask shows %d bytes for a(b)&c.png, written with markdown escapes, want its %d", len(got), len(escaped))
	}
	if got := askImage(t, app, fromFile.ID, filepath.Join(work, "notes", "nested.webp")); !bytes.Equal(got, nested) {
		t.Fatalf("the file ask shows %d bytes for nested.webp after it was deleted, want its %d, resolved beside the file", len(got), len(nested))
	}

	missing := s.Run(testworld.Invocation{Session: asker, Dir: s.Path(), Stdin: "Look: ![gone](work/beside.png)\n", Args: []string{"ask"}})
	if missing.Code == 0 || !strings.Contains(missing.Stderr, s.Path("work", "beside.png")) {
		t.Fatalf("an ask showing a missing image: %+v, want a refusal naming the path", missing)
	}

	writeImage(t, s.Path("big", "a.png"), ask.MaxImageBytes/2)
	writeImage(t, s.Path("big", "b.png"), ask.MaxImageBytes/2+1)
	over := s.Run(testworld.Invocation{Session: asker, Dir: s.Path("big"), Stdin: "![a](a.png) ![b](b.png)\n", Args: []string{"ask"}})
	want := fmt.Sprintf("max_ask_image_bytes=%d", ask.MaxImageBytes)
	if over.Code == 0 || !strings.Contains(over.Stderr, want) || !strings.Contains(over.Stderr, fmt.Sprintf("%d bytes", ask.MaxImageBytes+1)) {
		t.Fatalf("an ask over the image limit: %+v, want a refusal naming %s and the %d bytes asked for", over, want, ask.MaxImageBytes+1)
	}
	if listed, err := s.Client().AskList(protocol.SessionID(asker)); err != nil || len(listed.Asks) != 2 {
		t.Fatalf("after the refusals the agent lists %+v (%v), want only its two asks", listed, err)
	}
}

func TestAnAskOpenedBeforeImagesTravelledGetsItsImagesCopiedWhenTheDaemonStarts(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	turnAsksOn(app)
	asker := s.Spawn(app, fakeagent.Claude, s.Path("work"))
	s.Launched(asker)
	shot := writeImage(t, s.Path("work", "shot.png"), 1024)
	earlier, err := s.Client().AskOpen(protocol.SessionID(asker), "", "Before: ![shot](shot.png)\n", "", s.Path("work"))
	if err != nil {
		t.Fatal(err)
	}

	s.Stop()
	s.Start()
	removeAll(t, s.Path("work"))
	if got := askImage(t, s.App(), earlier.ID, s.Path("work", "shot.png")); !bytes.Equal(got, shot) {
		t.Fatalf("after the restart, the ask opened without images shows %d bytes for its image, want its %d", len(got), len(shot))
	}
}

func turnAsksOn(app *testworld.Peer) {
	testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "asks_enabled", Value: "true", RequestID: protocol.Ptr("asks-on")},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "asks-on" })
}

func askAs(t *testing.T, s *testworld.Stack, session, dir, stdin string, args ...string) protocol.Ask {
	t.Helper()
	opened := s.Run(testworld.Invocation{Session: session, Dir: dir, Stdin: stdin, Args: append([]string{"ask", "--json"}, args...)})
	if opened.Code != 0 {
		t.Fatalf("ask: %+v", opened)
	}
	var asked protocol.Ask
	opened.JSON(t, &asked)
	return asked
}

func askImageResult(app *testworld.Peer, askID, path string) protocol.AskImageResultMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.AskImageMessage{Cmd: protocol.CmdAskImage, RequestID: requestID, AskID: askID, Path: path},
		protocol.EventAskImageResult, func(r protocol.AskImageResultMessage) bool { return r.RequestID == requestID })
}

func askImage(t *testing.T, app *testworld.Peer, askID, path string) []byte {
	t.Helper()
	result := askImageResult(app, askID, path)
	if !result.Success {
		t.Fatalf("image %s of %s: %s", path, askID, protocol.Deref(result.Error))
	}
	data, err := base64.StdEncoding.DecodeString(protocol.Deref(result.DataBase64))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeImage(t *testing.T, path string, size int) []byte {
	t.Helper()
	data := bytes.Repeat([]byte(filepath.Base(path)), size/len(filepath.Base(path))+1)[:size]
	writeAskFile(t, path, string(data))
	return data
}

func writeAskFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removeAll(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}
