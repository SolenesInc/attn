package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
)

func newWakeableDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, func() string) {
	t.Helper()
	d := newCrewDaemon(t)
	binDir := t.TempDir()
	for _, agent := range []string{"claude", "codex"} {
		script := "#!/bin/sh\nexit 0\n"
		if agent == "claude" {
			script = "#!/bin/sh\nread request\nprintf '%s\\n' '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"attn-model-discovery\",\"response\":{\"models\":[{\"value\":\"fixture-model\",\"supportsEffort\":true}]}}}'\ncat >/dev/null\n"
		}
		if err := os.WriteFile(filepath.Join(binDir, agent), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s executable: %v", agent, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend := &fakeSpawnBackend{screen: "❯"}
	d.ptyBackend = backend

	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger

	return d, backend, func() string {
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read daemon log: %v", err)
		}
		return string(body)
	}
}

func TestCrewSet_ACwdInsideAnotherInstancesCrewIsRefused(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	userHome := t.TempDir()
	foreign := filepath.Join(userHome, ".attn-fixture", crew.HomesDirName, "ember", "project")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatalf("create foreign crew cwd: %v", err)
	}

	_, err := d.resolveCrewWorkDirForHome(foreign, userHome)
	if err == nil {
		t.Fatal("a cwd inside another instance's crew homes was accepted")
	}
	for _, want := range []string{foreign, filepath.Join(userHome, ".attn-fixture", crew.HomesDirName), filepath.Join(d.dataRoot, crew.HomesDirName)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestCrewSet_AMissingPathInsideAnotherInstancesCrewIsRefused(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	userHome := t.TempDir()
	foreignRoot := filepath.Join(userHome, ".attn-fixture", crew.HomesDirName)
	if err := os.MkdirAll(foreignRoot, 0o755); err != nil {
		t.Fatalf("create foreign crew root: %v", err)
	}
	missing := filepath.Join(foreignRoot, "quartz", "moved-project")

	_, err := d.resolveCrewWorkDirForHome(missing, userHome)
	if err == nil {
		t.Fatal("a missing path inside another instance's crew homes was accepted")
	}
	for _, want := range []string{missing, foreignRoot, filepath.Join(d.dataRoot, crew.HomesDirName)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestCrewSet_ASymlinkedForeignInstanceRootIsRefused(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	userHome := t.TempDir()
	foreignTarget := filepath.Join(t.TempDir(), "foreign-instance")
	foreign := filepath.Join(foreignTarget, crew.HomesDirName, "quartz", "project")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatalf("create symlinked foreign crew cwd: %v", err)
	}
	instanceLink := filepath.Join(userHome, ".attn-fixture")
	if err := os.Symlink(foreignTarget, instanceLink); err != nil {
		t.Fatalf("symlink foreign instance: %v", err)
	}
	linkedCWD := filepath.Join(instanceLink, crew.HomesDirName, "quartz", "project")

	_, err := d.resolveCrewWorkDirForHome(linkedCWD, userHome)
	if err == nil {
		t.Fatal("a cwd under a symlinked foreign instance root was accepted")
	}
	for _, want := range []string{linkedCWD, filepath.Join(userHome, ".attn-fixture", crew.HomesDirName), filepath.Join(d.dataRoot, crew.HomesDirName)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	canonicalCWD, err := filepath.EvalSymlinks(linkedCWD)
	if err != nil {
		t.Fatalf("canonicalize linked cwd: %v", err)
	}
	if _, err := d.resolveCrewWorkDirForHome(canonicalCWD, userHome); err == nil {
		t.Fatal("the canonical target of a symlinked foreign instance root was accepted")
	}
}

func TestCrewPrime_AClaimOlderThanAPageOfTheGardenStillWakesWithItsMember(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	d.ensureGardenCollections()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	planted := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	oldest := garden.Seed{
		ProfileID: defaultProfileID(t, d.store),
		ID:        "s-000000", Title: "The claim nobody released", Status: garden.StatusGrowing,
		StepSlug: "claim-nobody-released", TenderMember: "trellis",
		StateChangedAt: planted.Format(time.RFC3339Nano), Edges: []garden.Edge{}, Vars: []garden.Var{},
	}
	body, err := oldest.Encode()
	if err != nil {
		t.Fatalf("encode seed: %v", err)
	}
	if _, err := d.store.PutDocument(*schema, oldest.ID, body, planted, nil); err != nil {
		t.Fatalf("put seed %s: %v", oldest.ID, err)
	}
	for i := 1; i <= docstore.MaxLimit; i++ {
		id := fmt.Sprintf("s-%06x", i)
		seed := garden.Seed{
			ProfileID: defaultProfileID(t, d.store),
			ID:        id, Title: id, Status: garden.StatusPlanted, StepSlug: id,
			StateChangedAt: planted.Format(time.RFC3339Nano), Edges: []garden.Edge{}, Vars: []garden.Var{},
		}
		newer, err := seed.Encode()
		if err != nil {
			t.Fatalf("encode seed %s: %v", id, err)
		}
		if _, err := d.store.PutDocument(*schema, id, newer, planted.Add(time.Duration(i)*time.Minute), nil); err != nil {
			t.Fatalf("put seed %s: %v", id, err)
		}
	}

	result, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	_, block, _, err := d.crewPrimeForSession(result.SessionID)
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	if !strings.Contains(block, "`s-000000` claim-nobody-released — The claim nobody released") {
		t.Errorf("a claim older than one page of the garden was dropped from priming:\n%s", block)
	}
	if strings.Contains(block, "You hold no seeds in the garden") {
		t.Error("a member holding an older claim was told it holds nothing")
	}
}

func writeCrewHomes(t *testing.T, dataRoot string) {
	t.Helper()
	root := filepath.Join(dataRoot, crew.HomesDirName)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(root, "CREW.md"), "# The crew\n")
	for _, member := range []struct{ id, handoff string }{
		{"alder", "2026-08-10T19-20Z-alder.md"},
		{"keel", "2026-08-13T22-10Z-keel.md"},
		{"trellis", "2026-08-13T22-20Z-trellis.md"},
	} {
		home := filepath.Join(root, member.id)
		write(filepath.Join(home, crew.CharterFileName), "# "+member.id+"\n\nWhat I care about.\n")
		write(filepath.Join(home, "handoffs", member.handoff), "Where I left off.\n")
	}
}

func newCrewDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	return d
}

func crewList(t *testing.T, d *Daemon) []protocol.CrewMember {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleCrewList(c, &protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	})
	if !resp.Ok {
		t.Fatalf("crew list: %v", protocol.Deref(resp.Error))
	}
	return resp.CrewListResult.Members
}

func TestCrewWake_RefusesAMemberOfAnotherProfile(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	work := createTestProfile(t, d.store, "Work")
	d.store.Add(&protocol.Session{ID: "work-agent", Label: "Work agent", Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), ProfileID: work.ID})

	for _, msg := range []*protocol.CrewWakeMessage{
		{Member: "trellis", ProfileID: protocol.Ptr(work.ID)},
		{Member: "trellis", SourceSessionID: protocol.Ptr("work-agent")},
		{Member: "trellis", SourceSessionID: protocol.Ptr("work-agent"), ProfileID: protocol.Ptr(defaultProfileID(t, d.store))},
	} {
		if _, err := d.crewWakeAsked(msg); err == nil || !strings.Contains(err.Error(), work.ID) {
			t.Fatalf("wake %+v = %v, want a refusal naming profile %s", msg, err, work.ID)
		}
	}
	if spawnCount(backend) != 0 {
		t.Fatal("a refused wake spawned a session")
	}

	result, err := d.crewWakeAsked(&protocol.CrewWakeMessage{Member: "trellis", ProfileID: protocol.Ptr(defaultProfileID(t, d.store))})
	if err != nil || result.ProfileID != defaultProfileID(t, d.store) {
		t.Fatalf("wake from the member's own profile = %+v, %v", result, err)
	}
}
