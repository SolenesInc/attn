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
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func crewHandoff(t *testing.T, cli *client.Client, session, letter string, retry bool, close protocol.CrewDayClose) *protocol.CrewHandoffResult {
	t.Helper()
	result, err := cli.CrewHandoff(session, letter, retry, close)
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
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	return names
}

func TestAHandoffFilesTheLetterAndWakesTheNextDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	app := w.App()
	cli := w.Client()

	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(day.SessionID)
	letter := "Dear next trellis,\n\n#901 is waiting on review.\n"
	handed := crewHandoff(t, cli, day.SessionID, letter, false, "")
	successor := protocol.Deref(handed.SessionID)
	if protocol.Deref(handed.Outcome) != protocol.CrewDayCloseNap || handed.NapError != nil || successor == "" || successor == day.SessionID {
		t.Fatalf("handoff = %+v, want a nap into a fresh day", handed)
	}
	if body, err := os.ReadFile(handed.Path); err != nil || string(body) != letter {
		t.Fatalf("the filed letter holds %q (%v), want the member's prose untouched", body, err)
	}
	if letters := crewLetters(t, w, "trellis"); len(letters) != 2 || letters[0] != filepath.Base(handed.Path) {
		t.Fatalf("trellis's letters = %q, want the new one freshest", letters)
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == day.SessionID
	})
	next := w.Launched(successor)
	if next.Resumed {
		t.Error("the successor resumed the closed day's conversation")
	}
	if got, want := next.Prompted(), prompts.RenderText("crew", "successor", prompts.Values{}); got != want {
		t.Errorf("the successor was asked %q, want the successor prompt", got)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != successor {
		t.Fatalf("roster binding = %q, want the successor %s", got, successor)
	}
	primed := crewPriming(t, cli, successor)
	if !strings.Contains(primed, "#901 is waiting on review.") || !strings.Contains(primed, filepath.Base(handed.Path)) {
		t.Errorf("the successor's priming does not carry the letter just filed:\n%s", primed)
	}
	t.Run("a codex member's successor comes back on codex", func(t *testing.T) {
		setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
		day := wakeCrew(t, cli, "keel", "")
		w.Launched(day.SessionID)
		handed := crewHandoff(t, cli, day.SessionID, "Codex signing off: the fence lands first.", false, protocol.CrewDayCloseNap)
		next := w.Launched(protocol.Deref(handed.SessionID))
		if next.Harness != fakeagent.Codex || crewLaunchFlag(next.Argv, "--model") != "" {
			t.Fatalf("the successor launched %s with argv %q, want codex on its default model", next.Harness, next.Argv)
		}
		if primed := crewPriming(t, cli, protocol.Deref(handed.SessionID)); !strings.Contains(primed, "Codex signing off: the fence lands first.") {
			t.Errorf("the codex successor's priming does not carry the letter:\n%s", primed)
		}
	})

	t.Run("an explicit sleep ends the day with nobody behind it", func(t *testing.T) {
		day := wakeCrew(t, cli, "alder", "")
		w.Launched(day.SessionID)
		before := crewSessionCount(t, cli)
		slept := crewHandoff(t, cli, day.SessionID, "Signing off for the night.", false, protocol.CrewDayCloseSleep)
		if protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep || slept.SessionID != nil {
			t.Fatalf("sleep = %+v, want the day ended with no successor", slept)
		}
		if binding := crewRosterMember(t, cli, "alder").BindingSession; binding != nil {
			t.Fatalf("alder is still bound to %s after going to sleep", *binding)
		}
		if got := crewSessionCount(t, cli); got != before-1 {
			t.Fatalf("sessions after the sleep = %d, want the day gone from %d", got, before)
		}
	})
}

func TestAFailedNapKeepsTheLetterAndTheDayAndARetryTurnsItOver(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	wakeInDirectoryThatThenMoves := func(member string) (*protocol.CrewWakeResult, string) {
		t.Helper()
		workDir := w.Path(member + "-work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		setCrew(t, cli, member, protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir)})
		day := wakeCrew(t, cli, member, "")
		w.Launched(day.SessionID)
		if err := os.RemoveAll(workDir); err != nil {
			t.Fatal(err)
		}
		return day, workDir
	}

	day, workDir := wakeInDirectoryThatThenMoves("alder")
	failed := crewHandoff(t, cli, day.SessionID, "The letter, written once.", false, "")
	if failed.NapError == nil || failed.SessionID != nil {
		t.Fatalf("handoff from a day whose directory moved = %+v, want a nap error and no successor", failed)
	}
	if _, err := os.Stat(failed.Path); err != nil {
		t.Fatalf("the filed letter was rolled back: %v", err)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != day.SessionID {
		t.Fatalf("after the failed nap alder is bound to %q, want the running day %s", got, day.SessionID)
	}
	letters := crewLetters(t, w, "alder")
	var occupied []string
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		name := crew.HandoffFileName("alder", at)
		if !slices.Contains(letters, name) {
			occupied = append(occupied, writeCrewHomeFile(t, w, "alder", filepath.Join(crew.HandoffsDirName, name), "taken\n"))
		}
	}
	_, err := cli.CrewHandoff(day.SessionID, "The letter, written twice.", false, "")
	crewErrorContains(t, err, "--retry", failed.Path)
	for _, path := range occupied {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	retried := crewHandoff(t, cli, day.SessionID, "", true, "")
	successor := protocol.Deref(retried.SessionID)
	if protocol.Deref(retried.Outcome) != protocol.CrewDayCloseNap || retried.NapError != nil || successor == "" || retried.Path != failed.Path {
		t.Fatalf("retry = %+v, want a nap on the letter already filed at %s", retried, failed.Path)
	}
	w.Launched(successor)
	if after := crewLetters(t, w, "alder"); !slices.Equal(after, letters) {
		t.Fatalf("alder's letters went from %q to %q; a retry files nothing new", letters, after)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "alder").BindingSession); got != successor {
		t.Fatalf("after the retry alder is bound to %q, want the successor %s", got, successor)
	}
	if primed := crewPriming(t, cli, successor); !strings.Contains(primed, "The letter, written once.") {
		t.Errorf("the successor was not primed by the letter already filed:\n%s", primed)
	}

	t.Run("a retry can put the member to sleep instead", func(t *testing.T) {
		day, _ := wakeInDirectoryThatThenMoves("keel")
		if failed := crewHandoff(t, cli, day.SessionID, "Tonight's letter.", false, ""); failed.NapError == nil {
			t.Fatalf("handoff = %+v, want the nap to fail", failed)
		}
		slept := crewHandoff(t, cli, day.SessionID, "", true, protocol.CrewDayCloseSleep)
		if protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep {
			t.Fatalf("retry --sleep = %+v, want sleep", slept)
		}
		if binding := crewRosterMember(t, cli, "keel").BindingSession; binding != nil {
			t.Fatalf("keel is still bound to %s after being told to sleep", *binding)
		}
	})
}

func TestHandoffRefusalsLeaveTheDayRunning(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	if err := cli.Register("errand", "errand", w.Path("errand")); err != nil {
		t.Fatal(err)
	}
	_, err := cli.CrewHandoff("errand", "I did some work today.", false, "")
	crewErrorContains(t, err, "attn seed note")

	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(day.SessionID)
	_, err = cli.CrewHandoff(day.SessionID, "", true, "")
	crewErrorContains(t, err, "attn handoff -m")

	now := time.Now()
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		writeCrewHomeFile(t, w, "trellis", filepath.Join(crew.HandoffsDirName, crew.HandoffFileName("trellis", at)), "somebody else's closure\n")
	}
	_, err = cli.CrewHandoff(day.SessionID, "My letter.", false, "")
	crewErrorContains(t, err, "never overwritten")

	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != day.SessionID {
		t.Fatalf("after the refusals trellis is bound to %q, want the running day %s", got, day.SessionID)
	}
	if got := crewSessionCount(t, cli); got != 2 {
		t.Fatalf("sessions after the refusals = %d, want the errand and the running day", got)
	}

	keel := wakeCrew(t, cli, "keel", "")
	w.Launched(keel.SessionID)
	handed := crewHandoff(t, cli, keel.SessionID, "Filed and gone.", false, "")
	w.Launched(protocol.Deref(handed.SessionID))
	_, err = cli.CrewHandoff(protocol.Deref(handed.SessionID), "", true, "")
	crewErrorContains(t, err, "filed no letter yet")
}

func TestCrewLettersNeverLeaveTheMemberHome(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	day := wakeCrew(t, cli, "keel", "")
	w.Launched(day.SessionID)
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
	_, err := cli.CrewHandoff(day.SessionID, "This must stay in my instance.", false, "")
	crewErrorContains(t, err, handoffs, crewHome(w, "keel"), "symlink")
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatalf("the handoff wrote outside the member home: %v, %v", entries, err)
	}
	if got := crewSessionCount(t, cli); got != 1 {
		t.Fatalf("sessions after the refused handoff = %d, want only the running day", got)
	}

	alder := wakeCrew(t, cli, "alder", "")
	w.Launched(alder.SessionID)
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

func TestAutonomousWakesStopAtTheWakeLimit(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	setSetting(t, app, "crew.wake_limit", "2")
	if err := cli.Register("sender", "sender", w.Path("sender")); err != nil {
		t.Fatal(err)
	}

	for _, request := range []string{"please look at #901", "please look at #902"} {
		sent, err := cli.AgentMsg("trellis", "sender", request)
		if err != nil || sent.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("message to asleep trellis = %+v, %v; want it woken and queued", sent, err)
		}
		w.Launched(sent.TargetSessionID)
		if err := cli.Unregister(sent.TargetSessionID); err != nil {
			t.Fatal(err)
		}
	}
	before := crewSessionCount(t, cli)
	_, err := cli.AgentMsg("trellis", "sender", "please look at #903")
	crewErrorContains(t, err, "Trellis", "crew.wake_limit=2")
	if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
		t.Fatalf("a refused wake bound trellis to %s", *binding)
	}
	if got := crewSessionCount(t, cli); got != before {
		t.Fatalf("a refused wake changed the sessions from %d to %d", before, got)
	}
}
