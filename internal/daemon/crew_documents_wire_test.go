package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheCharterEditorWritesTheFileOrReturnsTheNewerEdit(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	charterPath := writeCrewHomeFile(t, w, "trellis", crew.CharterFileName, "# Trellis\n\nI keep the garden growing.\n")
	save := func(requestID, content, token string) protocol.CrewCharterSetResultMessage {
		t.Helper()
		msg := protocol.CrewCharterSetMessage{Cmd: protocol.CmdCrewCharterSet, Member: "trellis", Content: content, ExpectedToken: token}
		if requestID != "" {
			msg.RequestID = protocol.Ptr(requestID)
		}
		return testworld.Request(app, msg, protocol.EventCrewCharterSetResult, func(r protocol.CrewCharterSetResultMessage) bool { return r.RequestID == requestID })
	}
	onDisk := func() string {
		t.Helper()
		body, err := os.ReadFile(charterPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	read := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: "Trellis", RequestID: protocol.Ptr("read")},
		protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == "read" })
	if !read.Success || protocol.Deref(read.Member) != "trellis" || read.Charter == nil || read.Charter.Content != onDisk() || read.Charter.Token == "" {
		t.Fatalf("charter read = %+v, want the file on disk with a token", read)
	}

	edited := "# Trellis\n\nI keep the garden growing, one seed at a time.\n"
	written := save("write", edited, read.Charter.Token)
	if !written.Success || written.Conflict || written.Charter == nil || written.Charter.Token == read.Charter.Token || onDisk() != edited {
		t.Fatalf("save with the current token = %+v and the file holds %q, want the edit on disk under a new token", written, onDisk())
	}

	external := "# Trellis\n\nRewritten in another editor while the app had it open.\n"
	if err := os.WriteFile(charterPath, []byte(external), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := save("stale", "a stale local edit", written.Charter.Token)
	if !stale.Conflict || stale.Charter == nil || stale.Charter.Content != external || stale.Charter.Token == written.Charter.Token {
		t.Fatalf("stale save = %+v, want a conflict returning the external edit in full", stale)
	}
	unaddressed := save("", "must not be written", stale.Charter.Token)
	if unaddressed.Success || !strings.Contains(protocol.Deref(unaddressed.Error), "request id") {
		t.Fatalf("save without a request id = %+v, want it refused", unaddressed)
	}
	if onDisk() != external {
		t.Fatalf("refused saves reached the charter: %q", onDisk())
	}

	racers := map[string]*testworld.Peer{"racer-a": w.App(), "racer-b": w.App()}
	for requestID, peer := range racers {
		peer.Send(protocol.CrewCharterSetMessage{Cmd: protocol.CmdCrewCharterSet, Member: "trellis", Content: "# Trellis\n\nSaved by " + requestID + ".\n", ExpectedToken: stale.Charter.Token, RequestID: protocol.Ptr(requestID)})
	}
	var winners, conflicts []protocol.CrewCharterSetResultMessage
	for requestID, peer := range racers {
		result := testworld.Await(peer, protocol.EventCrewCharterSetResult, func(r protocol.CrewCharterSetResultMessage) bool { return r.RequestID == requestID })
		switch {
		case result.Success && !result.Conflict:
			winners = append(winners, result)
		case result.Conflict:
			conflicts = append(conflicts, result)
		}
	}
	if len(winners) != 1 || len(conflicts) != 1 {
		t.Fatalf("two saves on one token = %d written and %d conflicts, want one of each", len(winners), len(conflicts))
	}
	won := "# Trellis\n\nSaved by " + winners[0].RequestID + ".\n"
	if conflicts[0].Charter == nil || conflicts[0].Charter.Content != won || onDisk() != won {
		t.Fatalf("the losing save returned %+v and the file holds %q, want both to carry the winner %q", conflicts[0].Charter, onDisk(), won)
	}
}

func TestHandoffsListNewestFirstAndReadOnlyTheMembersLetters(t *testing.T) {
	w := newCrewWorld(t)
	app := w.App()
	list := func(member string) protocol.CrewHandoffsGetResultMessage {
		t.Helper()
		return testworld.Request(app, protocol.CrewHandoffsGetMessage{Cmd: protocol.CmdCrewHandoffsGet, Member: member, RequestID: protocol.Ptr("list-" + member)},
			protocol.EventCrewHandoffsGetResult, func(r protocol.CrewHandoffsGetResultMessage) bool { return r.RequestID == "list-"+member })
	}
	letter := func(filename string) protocol.CrewHandoffGetResultMessage {
		t.Helper()
		return testworld.Request(app, protocol.CrewHandoffGetMessage{Cmd: protocol.CmdCrewHandoffGet, Member: "trellis", Filename: filename, RequestID: protocol.Ptr("read-" + filename)},
			protocol.EventCrewHandoffGetResult, func(r protocol.CrewHandoffGetResultMessage) bool { return r.RequestID == "read-"+filename })
	}

	newest := "2026-09-01T21-37Z-trellis.md"
	body := "Dear next trellis,\n\nThe fence lands first.\n"
	writeCrewHomeFile(t, w, "trellis", filepath.Join(crew.HandoffsDirName, newest), body)
	listed := list("trellis")
	wantDate := time.Date(2026, 9, 1, 21, 37, 0, 0, time.UTC)
	if !listed.Success || len(listed.Handoffs) != 2 || listed.Handoffs[0].Filename != newest || !listed.Handoffs[0].OccurredAt.Equal(wantDate) ||
		listed.Handoffs[1].Filename != crewHomeLetters["trellis"] {
		t.Fatalf("trellis's handoffs = %+v, want %s dated %s ahead of %s", listed, newest, wantDate, crewHomeLetters["trellis"])
	}
	read := letter(newest)
	if !read.Success || read.Handoff == nil || read.Handoff.Filename != newest || read.Handoff.Content != body || read.Handoff.Token == "" || !read.Handoff.OccurredAt.Equal(wantDate) {
		t.Fatalf("reading %s = %+v, want it in full with its date", newest, read)
	}
	for _, outside := range []string{"", "../" + crew.CharterFileName, "notes.txt", "2026-09-01T21-37Z-other.md", "missing-2026.md"} {
		if refused := letter(outside); refused.Success {
			t.Errorf("handoff %q was readable: %+v", outside, refused)
		}
	}

	if err := os.RemoveAll(filepath.Join(crewHome(w, "keel"), crew.HandoffsDirName)); err != nil {
		t.Fatal(err)
	}
	if empty := list("keel"); !empty.Success || empty.Handoffs == nil || len(empty.Handoffs) != 0 {
		t.Fatalf("keel's empty history = %+v, want an empty list", empty)
	}
}
