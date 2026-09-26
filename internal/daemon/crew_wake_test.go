package daemon

import (
	"context"
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
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
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

func crewSet(t *testing.T, d *Daemon, msg protocol.CrewSetMessage) protocol.Response {
	t.Helper()
	msg.Cmd = protocol.CmdCrewSet
	return gardenCall(t, func(c net.Conn) { d.handleCrewSet(c, &msg) })
}

func TestCrewWake_AMemberWakesOnItsConfiguredModel(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingDefaultModelPrefix+"claude", "claude-sonnet-4-5")
	const qualifiedModel = "anthropic/claude-haiku-4-5"
	if resp := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Model: protocol.Ptr(qualifiedModel)}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}
	if _, err := d.crewWake("trellis", ""); err != nil {
		t.Fatalf("wake: %v", err)
	}

	backend.mu.Lock()
	model := backend.spawnOpts[0].Model
	backend.mu.Unlock()
	if model != qualifiedModel {
		t.Fatalf("member woke on model %q, want its configured model", model)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").Model); got != qualifiedModel {
		t.Fatalf("roster model = %q, want the configured model", got)
	}
}

func TestCrewWake_OneDayHarnessOverrideDoesNotTakeTheMembersUsualModel(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingDefaultModelPrefix+"codex", "gpt-5.6-sol")
	member := crew.Member{Agent: "claude", Model: "claude-haiku-4-5"}
	if got := protocol.Deref(d.crewWakeModel(member, "codex")); got != "gpt-5.6-sol" {
		t.Fatalf("one-day Codex override resolved to %q, want its configured default", got)
	}
}

type crewRuntimeBackend struct {
	*fakeSpawnBackend
	running map[string]bool
	infoErr map[string]error
}

func (b *crewRuntimeBackend) Spawn(ctx context.Context, opts ptybackend.SpawnOptions) error {
	if err := b.fakeSpawnBackend.Spawn(ctx, opts); err != nil {
		return err
	}
	b.running[opts.ID] = true
	return nil
}

func (b *crewRuntimeBackend) SessionInfo(_ context.Context, sessionID string) (ptybackend.SessionInfo, error) {
	if err := b.infoErr[sessionID]; err != nil {
		return ptybackend.SessionInfo{}, err
	}
	running, ok := b.running[sessionID]
	if !ok {
		return ptybackend.SessionInfo{}, pty.ErrSessionNotFound
	}
	return ptybackend.SessionInfo{SessionID: sessionID, Running: running}, nil
}

func TestCrewSet_RecordsReadsAndClearsAMembersModel(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	resp := crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Model: protocol.Ptr("gpt-5.6-sol")})
	if !resp.Ok {
		t.Fatalf("crew set model: %v", protocol.Deref(resp.Error))
	}
	if got := protocol.Deref(resp.CrewSetResult.Member.Model); got != "gpt-5.6-sol" {
		t.Fatalf("set result model = %q", got)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").Model); got != "gpt-5.6-sol" {
		t.Fatalf("roster model = %q", got)
	}

	resp = crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Model: protocol.Ptr("")})
	if !resp.Ok {
		t.Fatalf("clear model: %v", protocol.Deref(resp.Error))
	}
	if resp.CrewSetResult.Member.Model != nil {
		t.Fatalf("cleared model remains on set result: %q", *resp.CrewSetResult.Member.Model)
	}
	if got := memberByID(t, crewList(t, d), "keel").Model; got != nil {
		t.Fatalf("cleared model remains on roster: %q", *got)
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

func TestCrewWake_AnOutpostHoldsNoneOfIt(t *testing.T) {
	const home = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()

	_, err := d.crewWake("keel", "")
	if err == nil {
		t.Fatal("an outpost woke a crew member")
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("wake refusal %q does not name the home", err)
	}

	resp := crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Cwd: protocol.Ptr(t.TempDir())})
	if resp.Ok {
		t.Fatal("an outpost recorded crew state")
	}
	if !strings.Contains(protocol.Deref(resp.Error), home) {
		t.Errorf("set refusal %q does not name the home", protocol.Deref(resp.Error))
	}

	if _, _, bound, _ := d.crewPrimeForSession("sess-anything"); bound {
		t.Fatal("an outpost primed a session as a crew member")
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
		ID: "s-000000", Title: "The claim nobody released", Status: garden.StatusGrowing,
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
			ID: id, Title: id, Status: garden.StatusPlanted, StepSlug: id,
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
