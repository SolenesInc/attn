package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func crewHandoff(t *testing.T, cli *client.Client, session, letter string, retry bool) *protocol.CrewHandoffResult {
	t.Helper()
	result, err := cli.CrewHandoff(protocol.SessionID(session), letter, retry)
	if err != nil {
		t.Fatalf("handoff from %s: %v", session, err)
	}
	return result
}

func crewLetters(t *testing.T, w *world, member string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(crewHome(w, member), crew.HandoffsDirName))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	return names
}

func TestPlainHandoffSleepsWhileTheUserIsPresent(t *testing.T) {
	for _, v2 := range []string{"false", "true"} {
		t.Run("crew_v2_enabled="+v2, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				writeCrewCharter(t, w, "trellis")
				w.restart()
				app, cli := w.App(), w.Client()
				setSetting(t, app, "crew_v2_enabled", v2)
				presenceReport(app, protocol.SetClientPresenceMessage{Visible: true, IdleSeconds: protocol.Ptr(0.0)})
				presenceTierIs(t, cli, "before plain handoff", "present")
				day := wakeCrew(t, cli, "trellis", "")
				w.bootBubbleClaude(t, string(day.SessionID))
				before := crewSessionCount(t, cli)
				ended := crewHandoff(t, cli, string(day.SessionID), "The work is settled.", false)
				if protocol.Deref(ended.Outcome) != protocol.CrewDayCloseSleep || ended.SessionID != nil || ended.SuccessorError != nil {
					t.Fatalf("plain handoff = %+v, want sleep with no successor", ended)
				}
				testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
					return e.Session != nil && e.Session.ID == day.SessionID
				})
				if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
					t.Fatalf("plain handoff left trellis bound to %s", *binding)
				}
				if got := crewSessionCount(t, cli); got != before-1 {
					t.Fatalf("plain handoff left %d sessions, want %d", got, before-1)
				}
			})
		})
	}
}

func TestCrewCanFileTwoLettersWithinTheSameMinute(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		legacy := time.Now().UTC().Format(crew.HandoffStampLayout) + "-trellis.md"
		writeCrewHomeFile(t, w, "trellis", filepath.Join(crew.HandoffsDirName, legacy), "The old letter.\n")
		w.restart()
		cli, app := w.Client(), w.App()
		first := wakeCrew(t, cli, "trellis", "")
		w.bootBubbleClaude(t, string(first.SessionID))
		one := crewHandoff(t, cli, string(first.SessionID), "The first letter.\n", false)
		w.advance(time.Nanosecond)
		second := wakeCrew(t, cli, "trellis", "")
		w.bootBubbleClaude(t, string(second.SessionID))
		two := crewHandoff(t, cli, string(second.SessionID), "The second letter.\n", false)
		listed := testworld.Request(app, protocol.CrewHandoffsGetMessage{Cmd: protocol.CmdCrewHandoffsGet, Member: "trellis", RequestID: protocol.Ptr("letters")},
			protocol.EventCrewHandoffsGetResult, func(r protocol.CrewHandoffsGetResultMessage) bool { return r.RequestID == "letters" })
		want := []string{filepath.Base(two.Path), filepath.Base(one.Path), legacy}
		var names []string
		for _, letter := range listed.Handoffs {
			names = append(names, letter.Filename)
		}
		if !listed.Success || !slices.Equal(names, want) {
			t.Fatalf("history = %+v, want newest first: %q", listed, want)
		}
		for i, body := range []string{"The second letter.\n", "The first letter.\n", "The old letter.\n"} {
			read := testworld.Request(app, protocol.CrewHandoffGetMessage{Cmd: protocol.CmdCrewHandoffGet, Member: "trellis", Filename: want[i], RequestID: protocol.Ptr(want[i])},
				protocol.EventCrewHandoffGetResult, func(r protocol.CrewHandoffGetResultMessage) bool { return r.RequestID == want[i] })
			if !read.Success || read.Handoff == nil || read.Handoff.Content != body {
				t.Fatalf("read %s = %+v, want %q", want[i], read, body)
			}
		}
		next := wakeCrew(t, cli, "trellis", "")
		if primed := crewPriming(t, cli, string(next.SessionID)); !strings.Contains(primed, "The second letter.") || strings.Contains(primed, "The first letter.") {
			t.Fatalf("wake did not use the newer letter:\n%s", primed)
		}
	})
}

func TestAFailedSwitchKeepsTheLetterAndTheDayAndARetryTurnsItOver(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	setSetting(t, w.App(), "crew_v2_enabled", "true")
	cli := w.Client()
	wakeInDirectoryThatThenMoves := func(member string) (*protocol.CrewWakeResult, string) {
		t.Helper()
		workDir := w.Path(member + "-work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		setCrew(t, cli, member, protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir)})
		day := wakeCrew(t, cli, member, "")
		w.Launched(string(day.SessionID))
		switchCrewHarness(t, cli, member, "codex")
		if err := os.RemoveAll(workDir); err != nil {
			t.Fatal(err)
		}
		return day, workDir
	}

	day, workDir := wakeInDirectoryThatThenMoves("alder")
	failed := crewHandoff(t, cli, string(day.SessionID), "The letter, written once.", false)
	if failed.SuccessorError == nil || failed.SessionID != nil {
		t.Fatalf("handoff from a day whose directory moved = %+v, want a successor error and no successor", failed)
	}
	if _, err := os.Stat(failed.Path); err != nil {
		t.Fatalf("the filed letter was rolled back: %v", err)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != day.SessionID {
		t.Fatalf("after the failed switch alder is bound to %q, want the running day %s", got, day.SessionID)
	}
	letters := crewLetters(t, w, "alder")

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	retried := crewHandoff(t, cli, string(day.SessionID), "", true)
	successor := protocol.Deref(retried.SessionID)
	if protocol.Deref(retried.Outcome) != protocol.CrewDayCloseSuccessor || retried.SuccessorError != nil || successor == "" || retried.Path != failed.Path {
		t.Fatalf("retry = %+v, want a successor from the letter already filed at %s", retried, failed.Path)
	}
	w.Launched(string(successor))
	if after := crewLetters(t, w, "alder"); !slices.Equal(after, letters) {
		t.Fatalf("alder's letters went from %q to %q; a retry files nothing new", letters, after)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != successor {
		t.Fatalf("after the retry alder is bound to %q, want the successor %s", got, successor)
	}
	if primed := crewPriming(t, cli, string(successor)); !strings.Contains(primed, "The letter, written once.") {
		t.Errorf("the successor was not primed by the letter already filed:\n%s", primed)
	}

	t.Run("after a sleep request the retry puts the member to sleep", func(t *testing.T) {
		day, _ := wakeInDirectoryThatThenMoves("keel")
		if failed := crewHandoff(t, cli, string(day.SessionID), "Tonight's letter.", false); failed.SuccessorError == nil {
			t.Fatalf("handoff = %+v, want the successor to fail", failed)
		}
		before := crewSessionCount(t, cli)
		letters := crewLetters(t, w, "keel")
		sleepCrew(t, w, "keel")
		slept := crewHandoff(t, cli, string(day.SessionID), "", true)
		if protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep || slept.SessionID != nil {
			t.Fatalf("retry after the sleep request = %+v, want sleep with no successor", slept)
		}
		if binding := crewRosterMember(t, cli, "keel").BindingSession; binding != nil {
			t.Fatalf("keel is still bound to %s after being told to sleep", *binding)
		}
		if got := crewSessionCount(t, cli); got != before-1 {
			t.Fatalf("retry after the sleep request left %d sessions, want %d", got, before-1)
		}
		if after := crewLetters(t, w, "keel"); !slices.Equal(after, letters) {
			t.Fatalf("retry after the sleep request changed letters from %q to %q", letters, after)
		}
	})
}

func TestHandoffRefusalsLeaveTheDayRunning(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()

	if err := w.InjectSession("errand", "errand", w.Path("errand"), protocol.SessionAgentClaude); err != nil {
		t.Fatal(err)
	}
	_, err := cli.CrewHandoff("errand", "I did some work today.", false)
	crewErrorContains(t, err, "attn seed note")

	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	_, err = cli.CrewHandoff(day.SessionID, "", true)
	crewErrorContains(t, err, "attn handoff -m")
	_, err = cli.CrewHandoff(day.SessionID, "  \n\t ", false)
	crewErrorContains(t, err, "nothing to file")
	_, err = cli.CrewHandoff(day.SessionID, strings.Repeat("x", crew.MaxHandoffBytes+1), false)
	crewErrorContains(t, err, "64001", "64000")
	if letters := crewLetters(t, w, "trellis"); len(letters) != 1 {
		t.Fatalf("refused letters were filed: %q", letters)
	}

	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != day.SessionID {
		t.Fatalf("after the refusals trellis is bound to %q, want the running day %s", got, day.SessionID)
	}
	if got := crewSessionCount(t, cli); got != 2 {
		t.Fatalf("sessions after the refusals = %d, want the errand and the running day", got)
	}

	keel := wakeCrew(t, cli, "keel", "")
	w.Launched(string(keel.SessionID))
	switchCrewHarness(t, cli, "keel", "codex")
	handed := crewHandoff(t, cli, string(keel.SessionID), "Filed and gone.", false)
	w.Launched(string(protocol.Deref(handed.SessionID)))
	_, err = cli.CrewHandoff(protocol.Deref(handed.SessionID), "", true)
	crewErrorContains(t, err, "filed no letter yet")
}

func TestCrewLettersNeverLeaveTheMemberHome(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	day := wakeCrew(t, cli, "keel", "")
	w.Launched(string(day.SessionID))
	handoffs := filepath.Join(crewHome(w, "keel"), crew.HandoffsDirName)
	foreign := w.Path("foreign-handoffs")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(handoffs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, handoffs); err != nil {
		t.Fatal(err)
	}
	_, err := cli.CrewHandoff(day.SessionID, "This must stay in my instance.", false)
	crewErrorContains(t, err, handoffs, crewHome(w, "keel"), "symlink")
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatalf("the handoff wrote outside the member home: %v, %v", entries, err)
	}
	if got := crewSessionCount(t, cli); got != 1 {
		t.Fatalf("sessions after the refused handoff = %d, want only the running day", got)
	}

	alder := wakeCrew(t, cli, "alder", "")
	w.Launched(string(alder.SessionID))
	foreignLetter := filepath.Join(foreign, "foreign-letter.md")
	if err := os.WriteFile(foreignLetter, []byte("foreign words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(crewHome(w, "alder"), crew.HandoffsDirName, "2099-01-01T00-00Z-alder.md")
	if err := os.Symlink(foreignLetter, linked); err != nil {
		t.Fatal(err)
	}
	writeCrewHomeFile(t, w, "alder", filepath.Join(crew.HandoffsDirName, "2100-01-01T00-00Z-alder.md"), "newer local words\n")
	_, err = cli.CrewPrime(alder.SessionID)
	crewErrorContains(t, err, linked, "symlink")
}

func TestDeliveryWakesChargeTheLimitAndReportAnAlreadyUsedLimit(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.App(), w.Client()
		setSetting(t, app, "crew.wake_limit", "2")
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		sent := sendAgentMessage(t, cli, "sender", "trellis", "read across days")
		for attempt := 1; attempt <= 2; attempt++ {
			id := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
			day := w.bootBubbleClaude(t, string(id))
			day.reply("Ready. <!-- attn:state=idle -->")
			if day.promptsContaining(inboxDoorbell) != 1 {
				t.Fatalf("day %d did not ring", attempt)
			}
			if _, err := cli.CrewHandoff(id, "sleep unread", false); err != nil {
				t.Fatal(err)
			}
			if attempt == 1 {
				w.advance(5 * time.Minute)
			}
		}
		before := crewSessionCount(t, cli)
		fresh := sendAgentMessage(t, cli, "sender", "trellis", "a new item while the limit is used")
		if fresh.Status != protocol.AgentMsgStatusQueued || !strings.Contains(fresh.Detail, "crew.wake_limit=2") {
			t.Fatalf("used limit receipt=%+v", fresh)
		}
		w.advance(5 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("limit woke %s", *binding)
		}
		if got := crewSessionCount(t, cli); got != before {
			t.Fatalf("limit changed session count from %d to %d", before, got)
		}
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		mail := readInbox(t, cli, day.id, 0).Items
		if len(mail) != 2 || mail[0].ItemID != sent.MessageID {
			t.Fatalf("manual wake lost mail=%+v", mail)
		}
	})
}
