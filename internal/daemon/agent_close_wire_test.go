package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheChiefOfStaffClosesAnySessionButItself(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("chief"), w.Path("stranger"))
	chief, stranger := panes[0].session, panes[1].session
	if made := setChiefOfStaff(app, chief, true); !made.Success {
		t.Fatalf("making chief the chief of staff = %+v", made)
	}

	closed, err := cli.AgentClose(stranger, chief, "abandoned, nobody is driving it")
	if err != nil {
		t.Fatalf("the chief closes a stranger: %v", err)
	}
	if closed.Rule != protocol.AgentCloseRuleChiefOfStaff || closed.TargetSessionID != stranger {
		t.Errorf("close = %+v, want stranger closed under the chief_of_staff rule", closed)
	}
	if by := protocol.Deref(showSession(t, cli, stranger).ClosedBy); by != chief {
		t.Errorf("the ledger names %q as the closer, want the chief", by)
	}

	_, err = cli.AgentClose(chief, chief, "done for the day")
	agentCloseRefused(t, err, "session_close_protected", "chief of staff is protected from closing; unset the chief role first")
	if live := queriedIDs(t, cli, ""); !slices.Contains(live, chief) {
		t.Errorf("live sessions = %v, want the chief still there", live)
	}
}

func TestACrewMembersDayCannotBeClosedByAnAgent(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(day.SessionID)
	chief := spawnPanes(w, app, w.Path("chief"))[0].session
	if made := setChiefOfStaff(app, chief, true); !made.Success {
		t.Fatalf("making chief the chief of staff = %+v", made)
	}

	_, err := cli.AgentClose(day.SessionID, chief, "looks finished to me")
	agentCloseRefused(t, err, "session_close_protected", "Trellis is protected from closing; put Trellis to sleep first")
	if live := queriedIDs(t, cli, ""); !slices.Contains(live, day.SessionID) {
		t.Errorf("live sessions = %v, want the crew day still there", live)
	}
}

func TestAgentCloseBySeedClosesItsTender(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	orchestrator := spawnPanes(w, app, w.Path("orchestrator"))[0].session
	cwd := w.Path("orchestrator", "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := plantDelegationSeed(t, cli, orchestrator, "Reach it by seed")
	delegated, err := cli.Delegate(delegateAtSeed(orchestrator, cwd, seed))
	if err != nil {
		t.Fatalf("delegate at %s: %v", seed, err)
	}
	w.Launched(delegated.SessionID)

	closed, err := cli.AgentClose(seed, orchestrator, "its report landed")
	if err != nil {
		t.Fatalf("close by seed: %v", err)
	}
	if closed.TargetSessionID != delegated.SessionID || closed.Rule != protocol.AgentCloseRuleDispatcher {
		t.Errorf("close = %+v, want the seed's tender %s closed by its dispatcher", closed, delegated.SessionID)
	}
	if entry := showSession(t, cli, delegated.SessionID); protocol.Deref(entry.ClosedBy) != orchestrator || protocol.Deref(entry.CloseReason) != "its report landed" {
		t.Errorf("ledger entry = %+v, want it closed by the orchestrator for its reason", entry)
	}
}

func TestAgentCloseRefusalsCloseNothing(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("chief"), w.Path("worker"), w.Path("other"))
	chief, worker, other := panes[0].session, panes[1].session, panes[2].session
	dupes := []string{
		w.Spawn(app, fakeagent.Claude, w.Path("dupe"), withIDPrefix("dupe-")),
		w.Spawn(app, fakeagent.Claude, w.Path("dupe"), withIDPrefix("dupe-")),
	}
	for _, dupe := range dupes {
		w.Launched(dupe)
	}
	if made := setChiefOfStaff(app, chief, true); !made.Success {
		t.Fatalf("making chief the chief of staff = %+v", made)
	}
	seedling := strings.Repeat("🌱", 400)

	for _, row := range []struct {
		name, target, source, reason string
		code                         string
		wants                        []string
	}{
		{name: "a blank reason", target: worker, source: worker, reason: "   ", code: "close_reason_required", wants: []string{"reason"}},
		{name: "a reason past the limit", target: worker, source: worker, reason: strings.Repeat("x", 401), code: "close_reason_required", wants: []string{"401", "400"}},
		{name: "a reason past the limit in characters", target: other, source: other, reason: seedling + "🌱", code: "close_reason_required", wants: []string{"401", "400"}},
		{name: "an ambiguous prefix", target: "dupe", source: chief, reason: "tidying up", code: "ambiguous_session", wants: []string{"more than one session"}},
		{name: "a caller that is not a session", target: worker, source: "ghost", reason: "cleaning up", code: "sender_session_not_found"},
	} {
		_, err := cli.AgentClose(row.target, row.source, row.reason)
		if code := client.ErrorCode(err); code != row.code {
			t.Errorf("%s: close = %v, want code %s", row.name, err, row.code)
			continue
		}
		for _, want := range row.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not name %q", row.name, err, want)
			}
		}
	}
	if live := queriedIDs(t, cli, ""); !slices.Equal(agentCloseSortedIDs(live), agentCloseSortedIDs(append([]string{chief, worker, other}, dupes...))) {
		t.Fatalf("live sessions after the refusals = %v, want every session still there", live)
	}

	if _, err := cli.AgentClose(worker, worker, seedling); err != nil {
		t.Fatalf("a reason of exactly 400 characters was refused: %v", err)
	}
}

func agentCloseRefused(t *testing.T, err error, code, message string) {
	t.Helper()
	if got := client.ErrorCode(err); got != code || !strings.Contains(err.Error(), message) {
		t.Fatalf("close = %v (code %q), want %s saying %q", err, got, code, message)
	}
}

func agentCloseSortedIDs(ids []string) []string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	return sorted
}
