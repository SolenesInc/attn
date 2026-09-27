package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostFencesEveryCrewCommandAndShowsNoCrew(t *testing.T) {
	t.Parallel()
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	writeCharter(t, s, "keel")
	letters := filepath.Join(s.Dir, crew.HomesDirName, "keel", crew.HandoffsDirName)
	s.Start()
	if roster := crewRoster(t, s); len(roster) != 1 {
		t.Fatalf("the home's roster = %v, want keel", roster)
	}
	s.Stop()
	if enrolled := s.Attn("enrollment", "enroll", "--home", home); enrolled.Code != 0 {
		t.Fatalf("enroll exited %d: %s", enrolled.Code, enrolled.Stderr)
	}
	s.Start()

	for _, command := range [][]string{
		{"crew", "list"},
		{"crew", "wake", "keel"},
		{"crew", "sleep", "keel"},
		{"crew", "restart", "keel", "--request-id", "from-the-outpost"},
		{"crew", "set", "keel", "--cwd", s.Dir},
		{"handoff", "--session", "sess-outpost", "-m", "Filed from an outpost."},
	} {
		fenced := s.Attn(command...)
		if fenced.Code == 0 {
			t.Errorf("attn %v answered on an outpost: %s", command, fenced.Stdout)
			continue
		}
		requireLines(t, "attn "+command[0]+" "+command[1]+" on an outpost", fenced.Stderr, crew.Surface, home, enrollment.PlanPath)
	}
	if _, err := os.Stat(letters); !os.IsNotExist(err) {
		t.Errorf("an outpost filed a letter into keel's home: %v", err)
	}

	cli := s.Client()
	if err := cli.RegisterAsMember("sess-outpost", "outpost", s.Path("outpost"), "", "keel"); err == nil {
		t.Error("an outpost bound a session to keel")
	}
	if prime, err := cli.CrewPrime("sess-outpost"); err == nil && protocol.Deref(prime.Member) != "" {
		t.Errorf("an outpost primed a session as %s", protocol.Deref(prime.Member))
	}

	app := s.App()
	if len(app.Initial.Crew) != 0 {
		t.Errorf("an outpost's initial state carries %d crew members, want none", len(app.Initial.Crew))
	}
	charter := testworld.Request(app, protocol.CrewCharterGetMessage{Cmd: protocol.CmdCrewCharterGet, Member: "keel", RequestID: protocol.Ptr("charter")},
		protocol.EventCrewCharterGetResult, func(r protocol.CrewCharterGetResultMessage) bool { return r.RequestID == "charter" })
	if charter.Success || charter.Charter != nil {
		t.Errorf("an outpost served keel's charter: %+v", charter)
	}
	requireLines(t, "the charter refusal", protocol.Deref(charter.Error), home)
}
