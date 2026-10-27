package daemon_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func chiefModel(h fakeagent.Harness) string {
	if h == fakeagent.Codex {
		return "gpt-6.1-sol"
	}
	if h == fakeagent.Pi {
		return "test-model"
	}
	return "sonnet"
}

func configureChief(t *testing.T, w *world, h fakeagent.Harness, model string) string {
	return configureChiefOn(t, w, w.App(), h, model)
}

func configureChiefOn(t *testing.T, w *world, app *testworld.Peer, h fakeagent.Harness, model string) string {
	t.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(app, protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, RequestID: protocol.Ptr(requestID), Member: "chief", Agent: protocol.Ptr(string(h)), Model: protocol.Ptr(model)}, protocol.EventCrewSetResult, func(m protocol.CrewSetResultMessage) bool { return m.RequestID == requestID })
	if !result.Success || result.WokeSessionID == nil {
		t.Fatalf("configure Chief: error=%s wake_error=%s %+v", protocol.Deref(result.Error), protocol.Deref(result.WakeError), result)
	}
	return string(*result.WokeSessionID)
}

func TestEveryProfileHasItsOwnUnconfiguredChief(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	defaultProfile := app.SelectedProfile()
	if len(app.Initial.Desktops) != 1 {
		t.Fatalf("unconfigured Chief created a desktop: %+v", app.Initial.Desktops)
	}
	if app.Initial.MigrationPhase != nil && *app.Initial.MigrationPhase != protocol.MigrationPhaseComplete {
		t.Fatalf("fresh Chief opened legacy migration: %+v", app.Initial.MigrationPhase)
	}
	var chief protocol.CrewMember
	for _, m := range app.Initial.Crew {
		if m.Chief {
			chief = m
		}
	}
	if chief.Name != "Chief" || chief.Agent != nil || chief.ResolvedAgent != nil || chief.BindingSession != nil {
		t.Fatalf("fresh Chief: %+v", chief)
	}
	if chief.HomeDir != "" || chief.CharterPath != "" {
		t.Fatalf("unconfigured Chief already has a home: %+v", chief)
	}
	work := createProfile(app, "Work")
	side := w.AppOn(work.ID)
	var workChief protocol.CrewMember
	for _, m := range side.Initial.Crew {
		if m.Chief {
			workChief = m
		}
	}
	if len(side.Initial.Desktops) != 1 || workChief.Key == "" || workChief.Key == chief.Key {
		t.Fatalf("Work Chief: %+v", workChief)
	}
	w.restart()
	for _, m := range w.AppOn(defaultProfile).Initial.Crew {
		if m.Chief && m.Key != chief.Key {
			t.Fatalf("Default Chief changed: %+v", m)
		}
	}
	for _, m := range w.AppOn(work.ID).Initial.Crew {
		if m.Chief && m.Key != workChief.Key {
			t.Fatalf("Work Chief changed: %+v", m)
		}
	}
}

func TestAnUnconfiguredChiefWaitsForItsHarnessAndModel(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	registerSessions(t, w, cli, "sender")
	mail, err := cli.AgentMsg("chief", protocol.SessionID("sender"), "keep this until configured")
	if err != nil || !strings.Contains(mail.Detail, "no harness") {
		t.Fatalf("unconfigured mail: %+v, %v", mail, err)
	}
	_, err = cli.CrewWake("chief", "", "")
	crewErrorContains(t, err, "attn crew set chief", "--model")
	for _, m := range crewRoster(t, cli) {
		if m.Chief && m.BindingSession != nil {
			t.Fatalf("unconfigured Chief launched: %+v", m)
		}
	}
	for _, change := range []struct {
		agent string
		model *string
		want  string
	}{
		{"", nil, "needs a harness"},
		{"claude", nil, "needs a model"},
	} {
		_, err := cli.CrewSet("chief", nil, &change.agent, change.model, nil, nil)
		crewErrorContains(t, err, change.want, "attn crew set chief", "--agent", "--model")
	}
	id := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	chiefDesktop, _ := placedPane(t, w, id)
	if chiefDesktop.Name != "Chief" || chiefDesktop.ID == app.Initial.Desktops[0].ID {
		t.Fatalf("first wake did not create Chief's office: %+v", chiefDesktop)
	}
	run := w.Launched(id)
	if guidance := launchGuidance(t, run); !strings.Contains(guidance, "You are this profile's Chief") || !strings.Contains(guidance, "CHARTER.md") {
		t.Fatalf("chief launch guidance: %s", guidance)
	}
	if prompt := run.Prompted(); !strings.Contains(prompt, "You have been woken") {
		t.Fatalf("chief first prompt: %s", prompt)
	}
	run.Reply("Ready. <!-- attn:state=idle -->")
	if prompt := run.Prompted(); !strings.Contains(prompt, "attn agent inbox") {
		t.Fatalf("mail did not ring Chief: %s", prompt)
	}
	batch, err := cli.AgentInboxBatch(protocol.SessionID(id), 0)
	if err != nil || len(batch.Items) != 1 || !strings.Contains(batch.Items[0].Content, "keep this until configured") {
		t.Fatalf("Chief mail: %+v %v", batch, err)
	}
	for key, want := range map[string]string{"claude_cap_launch_instructions": "true", "codex_cap_launch_instructions": "true"} {
		if got := w.App().Initial.Settings[key]; got != want {
			t.Errorf("%s=%q, want %q", key, got, want)
		}
	}
}

func TestChiefMailWakesAfterSleepAndRename(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	id := configureChief(t, w, fakeagent.Claude, "sonnet")
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	slept, err := cli.CrewHandoff(protocol.SessionID(id), "Return when mail arrives.", false, protocol.CrewDayCloseSleep, "")
	if err != nil || protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep {
		t.Fatalf("sleep: %+v %v", slept, err)
	}
	if _, err := cli.CrewRename("chief", "Alfred"); err != nil {
		t.Fatal(err)
	}
	sent, err := cli.AgentMsg("chief", "sender", "the release landed")
	if err != nil {
		t.Fatal(err)
	}
	var next protocol.CrewMember
	for _, m := range crewRoster(t, cli) {
		if m.Chief {
			next = m
		}
	}
	if next.BindingSession == nil || string(*next.BindingSession) == id || next.Name != "Alfred" || sent.To != protocol.AddressRef("member:"+next.Key) {
		t.Fatalf("Chief wake after rename: %+v %+v", next, sent)
	}
	awakened := w.Launched(string(*next.BindingSession))
	awakened.Prompted()
	awakened.Reply("Ready. <!-- attn:state=idle -->")
	if prompt := awakened.Prompted(); !strings.Contains(prompt, "attn agent inbox") {
		t.Fatalf("mail did not ring: %s", prompt)
	}
	_, err = cli.CrewRename("chief", "Chief")
	crewErrorContains(t, err, "is awake", "attn crew sleep")
	if _, err := cli.CrewRetire("chief"); err == nil || !strings.Contains(err.Error(), "can't be retired") {
		t.Fatalf("retire Chief: %v", err)
	}
}

func TestChiefClearKeepsItsRoleAndAuthority(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	id := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	worker := spawnPanes(w, app, w.Path("worker"))[0].session
	next := clearClaude(app, run, id)
	if !protocol.Deref(next.Chief) {
		t.Fatalf("Chief /clear lost role: %+v", next)
	}
	app.TypeLine(string(next.ID), "orient again")
	run.Prompted()
	working := testworld.AwaitSession(app, string(next.ID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Ready again. <!-- attn:state=idle -->")
	settled := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if protocol.Deref(settled.TurnOwed) {
		t.Fatal("Chief owes a turn after /clear")
	}
	closed, err := cli.AgentClose(worker, next.ID, "the worker is finished")
	if err != nil || closed.Rule != protocol.AgentCloseRuleChief {
		t.Fatalf("successor Chief authority: %+v %v", closed, err)
	}
}

func TestChiefNeverHeartbeatsOrAutoSleepsAndPlainHandoffNaps(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, regular := crewDayInBubble(t, w, map[string]string{"crew.away_seconds": "60"})
		id := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
		chief := w.bootBubbleClaude(t, id)
		chief.reply("Ready. <!-- attn:state=idle -->")
		w.advance(56 * time.Minute)
		if len(regular.term.Pasted()) == 0 {
			t.Fatal("ordinary member did not receive its heartbeat")
		}
		if got := chief.term.Pasted(); len(got) != 0 {
			t.Fatalf("Chief received heartbeat: %q", got)
		}
		regular.reply("Still here. <!-- attn:state=idle -->")
		app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
		w.advance(0)
		app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: false, IdleSeconds: protocol.Ptr(120.0)})
		w.advance(2 * time.Hour)
		asleepAsk := readInbox(t, w.Client(), regular.id, 0).Items
		if len(asleepAsk) != 1 || !strings.Contains(asleepAsk[0].Content, prompts.RenderText("crew", "sleep-away", nil)) {
			t.Fatalf("ordinary member was not asked to sleep: %+v", asleepAsk)
		}
		if got := chief.term.Pasted(); len(got) != 0 {
			t.Fatalf("Chief received auto-sleep mail: %q", got)
		}
		handoff, err := w.Client().CrewHandoff(protocol.SessionID(id), "A fresh Chief session should continue.", false, "", "")
		if err != nil || protocol.Deref(handoff.Outcome) != protocol.CrewDayCloseNap || handoff.SessionID == nil {
			t.Fatalf("plain Chief handoff: %+v %v", handoff, err)
		}
	}, fakeagent.Claude)
}

func prepareChiefUpgrade(t *testing.T, missingCWD, emptyModel bool, bound ...bool) *world {
	return prepareChiefUpgradeAs(t, missingCWD, emptyModel, fakeagent.Codex, nil, bound...)
}

func prepareChiefUpgradeAs(t *testing.T, missingCWD, emptyModel bool, harness fakeagent.Harness, beforeStart func(*world), bound ...bool) *world {
	t.Helper()
	w := &world{World: prepareWorld(t, fakeagent.Codex, harness)}
	cwd := w.Path("old-chief")
	if !missingCWD {
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 1793111476175147-1)
	if err != nil {
		t.Fatal(err)
	}
	var profile string
	if err := db.QueryRow("SELECT id FROM profiles WHERE deleted_at=''").Scan(&profile); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-10-10T10:00:00Z"
	intent := `{"model":"intent-model","effort":"high"}`
	if emptyModel {
		intent = `{"effort":"high"}`
	}
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(id,user_label,directory,created_at,agent,launch_intent,profile_id,launched_at) VALUES('previous-chief','Chief',?,?,?,?,?,?)", []any{cwd, stamp, string(harness), intent, profile, stamp}},
		{"INSERT INTO session_runtime(session_id,state,state_since,state_updated_at,last_seen,agent_metadata) VALUES('previous-chief','idle',?,?,?,'recorded-conversation')", []any{stamp, stamp, stamp}},
		{"UPDATE profiles SET chief_session_id='previous-chief' WHERE id=?", []any{profile}},
		{"INSERT INTO inbox_items(id,address,kind,text,created_at) VALUES('before-upgrade',?,'notice','Saved before the upgrade',?)", []any{"chief:" + profile, stamp}},
	} {
		if _, err := db.Exec(row.sql, row.args...); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	previousParty := "session:previous-chief"
	historicDocument := func(namespace, collection, id string, body map[string]any) {
		t.Helper()
		var collectionID int64
		if err := db.QueryRow("INSERT INTO document_collections(namespace,collection,fields_json,updated_at) VALUES(?,?,'[]',?) ON CONFLICT(namespace,collection) DO UPDATE SET updated_at=excluded.updated_at RETURNING id", namespace, collection, stamp).Scan(&collectionID); err != nil {
			t.Fatal(err)
		}
		table := docstore.TableName(collectionID)
		if _, err := db.Exec(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s(id TEXT PRIMARY KEY,body TEXT NOT NULL,rev INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL) WITHOUT ROWID", table)); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO "+table+"(id,body,rev,created_at,updated_at) VALUES(?,?,1,?,?)", id, string(encoded), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if len(bound) > 0 && bound[0] {
		previousParty = "member:keel"
		home := filepath.Join(w.Dir, "crew", "keel")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "CHARTER.md"), []byte("# Keel\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO crew_members(member_key,profile_id,name) VALUES('keel',?,'Keel')", profile); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE sessions SET member_key='keel',user_label='Keel' WHERE id='previous-chief'"); err != nil {
			t.Fatal(err)
		}
		historicDocument(crew.Namespace, crew.CollectionMembers, "keel", map[string]any{"home_dir": home, "charter_path": filepath.Join(home, "CHARTER.md"), "cwd": cwd, "agent": "codex", "binding_session": "previous-chief"})
	}
	historicDocument(garden.Namespace, garden.CollectionSeeds, "s-c1am01", map[string]any{"id": "s-c1am01", "profile_id": profile, "title": "Inherited work", "body": "Finish this work.", "status": "growing", "tender": previousParty, "state_changed_at": stamp})
	if _, err := db.Exec("INSERT INTO garden_seed_watches(watcher,seed_id,created_at) VALUES(?, 's-c1am01', ?)", previousParty, stamp); err != nil {
		t.Fatal(err)
	}
	if !emptyModel {
		if _, err := db.Exec("INSERT INTO settings(key,value) VALUES('chief_model_codex','gpt-6.1-sol')"); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if beforeStart != nil {
		beforeStart(w)
	}
	w.start()
	return w
}

func TestUpgradeCreatesAnOnboardedChiefAndWaitsForTheUser(t *testing.T) {
	w := prepareChiefUpgrade(t, false, false)
	app, cli := w.App(), w.Client()
	var chief protocol.CrewMember
	for _, m := range app.Initial.Crew {
		if m.Chief {
			chief = m
		}
	}
	if protocol.Deref(chief.Agent) != "codex" || protocol.Deref(chief.Model) != "gpt-6.1-sol" || protocol.Deref(chief.Effort) != "high" || protocol.Deref(chief.Cwd) != filepath.Join(w.Dir, "notebook") || chief.BindingSession != nil {
		t.Fatalf("upgraded Chief before presence: %+v", chief)
	}
	claim, err := cli.SeedShow("previous-chief", "s-c1am01")
	if err != nil || claim.Seed.Tender == nil || claim.Seed.Tender.Ref != "session:previous-chief" || !claim.Watching {
		t.Fatalf("old session lost claim/watch: %+v %v", claim, err)
	}
	previous := showSession(t, cli, "previous-chief")
	if previous.Label != "Chief (previous)" || previous.MemberKey != nil {
		t.Fatalf("previous Chief: %+v", previous)
	}
	for _, session := range app.Initial.Sessions {
		if session.ID == "previous-chief" && protocol.Deref(session.Chief) {
			t.Fatal("previous Chief kept its powers")
		}
	}
	for key := range app.Initial.Settings {
		if strings.HasPrefix(key, "chief_") {
			t.Fatalf("removed chief setting %s still exists", key)
		}
	}
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := *testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool {
		return m.Session != nil && protocol.Deref(m.Session.Chief)
	}).Session
	run := w.Launched(string(awake.ID))
	run.Prompted()
	run.Reply("Ready to take over. <!-- attn:state=idle -->")
	if got := run.Prompted(); !strings.Contains(got, "attn agent inbox") {
		t.Fatalf("handover did not ring: %s", got)
	}
	items := readInbox(t, cli, string(awake.ID), 0).Items
	if len(items) != 2 {
		t.Fatalf("upgrade inbox: %+v", items)
	}
	content := ""
	for _, item := range items {
		if item.Address != protocol.AddressRef("member:"+chief.Key) {
			t.Fatalf("old Chief address remains: %s", item.Address)
		}
		content += item.Content
	}
	if !strings.Contains(content, "Tended: s-c1am01") || !strings.Contains(content, "Watched: s-c1am01") || !strings.Contains(content, "previous-chief") || !strings.Contains(content, "attn session transcript") || !strings.Contains(content, "Saved before the upgrade") {
		t.Fatalf("handover mail: %s", content)
	}
}

func TestUpgradePreservesTheOldChiefsMemberAndItsWork(t *testing.T) {
	w := prepareChiefUpgrade(t, false, false, true)
	app, cli := w.App(), w.Client()
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := *testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && protocol.Deref(m.Session.Chief) }).Session
	run := w.Launched(string(awake.ID))
	run.Prompted()
	run.Reply("Ready to take over. <!-- attn:state=idle -->")
	run.Prompted()
	items := readInbox(t, cli, string(awake.ID), 0)
	var handover string
	for _, item := range items.Items {
		if strings.Contains(item.Content, "You are this profile's Chief now") {
			handover = item.Content
		}
	}
	if !strings.Contains(handover, "Keel's session, previous-chief") || !strings.Contains(handover, "Keel remains a crew member and keeps its seeds and watches") || !strings.Contains(handover, "member:keel") || strings.Contains(handover, "--force") || strings.Contains(handover, "Tell it to stop") {
		t.Fatalf("member-owned handover: %s", handover)
	}
	previous := showSession(t, cli, "previous-chief")
	if previous.Label != "Keel" || protocol.Deref(previous.MemberKey) != "keel" {
		t.Fatalf("previous member changed: %+v", previous)
	}
	keel := wakeCrew(t, cli, "Keel", "")
	claim, err := cli.SeedShow(keel.SessionID, "s-c1am01")
	if err != nil || claim.Seed.Tender == nil || claim.Seed.Tender.Ref != "member:keel" || !claim.Watching {
		t.Fatalf("member lost claim/watch: %+v %v", claim, err)
	}
}

func TestUpgradeStartsTheChiefInTheNotebookEvenWhenItsOldDirectoryIsGone(t *testing.T) {
	w := prepareChiefUpgrade(t, true, false)
	app := w.App()
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := *testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && protocol.Deref(m.Session.Chief) }).Session
	w.Launched(string(awake.ID))
	if awake.Directory != filepath.Join(w.Dir, "notebook") {
		t.Fatalf("upgraded cwd: %s", awake.Directory)
	}
}

func TestChiefOneDayHarnessOverrideKeepsTheMembersPinsOnTheirHarness(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := configureChiefOn(t, w, app, fakeagent.Claude, "opus")
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if _, err := cli.CrewHandoff(protocol.SessionID(id), "Rest until the next request.", false, protocol.CrewDayCloseSleep, ""); err != nil {
		t.Fatal(err)
	}
	setSetting(t, app, "default_model_codex", "gpt-6.1-sol")
	next := wakeCrew(t, cli, "chief", "codex")
	launched := w.Launched(string(next.SessionID))
	model, _ := flagValue(launched.Argv, "--model")
	if launched.Harness != fakeagent.Codex || model != "gpt-6.1-sol" {
		t.Fatalf("Chief override carried another harness's model: %q", launched.Argv)
	}
	for _, m := range crewRoster(t, cli) {
		if m.Chief && (protocol.Deref(m.Agent) != "claude" || protocol.Deref(m.Model) != "opus") {
			t.Fatalf("one-day override rewrote Chief settings: %+v", m)
		}
	}
}

func TestDeletingAProfileRetiresItsChiefAndAnAwakeChiefRefusesDeletion(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	work := createProfile(app, "Work")
	selectProfile(app, work.ID)
	id := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	key := protocol.Deref(queriedSession(t, w.Client(), id).CrewMember)
	remove := func() protocol.ProfileActionResultMessage {
		requestID := uuid.NewString()
		return testworld.Request(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: requestID, ProfileID: work.ID, ExpectedRevision: work.Revision}, protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == requestID })
	}
	if refused := remove(); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "put the chief to sleep first: attn crew sleep chief") {
		t.Fatalf("delete with awake Chief: %+v", refused)
	}
	if _, err := w.Client().CrewHandoff(protocol.SessionID(id), "The profile is being removed.", false, protocol.CrewDayCloseSleep, ""); err != nil {
		t.Fatal(err)
	}
	if deleted := remove(); !deleted.Success {
		t.Fatalf("delete with sleeping Chief: %+v", deleted)
	}
	for _, m := range w.App().Initial.Crew {
		if m.Key == key {
			t.Fatalf("deleted Chief remains in roster: %+v", m)
		}
	}
	cli := w.Client()
	registerSessions(t, w, cli, "sender")
	if _, err := cli.AgentMsg("member:"+key, "sender", "this profile is gone"); err == nil {
		t.Fatal("deleted Chief still accepts mail")
	}
	w.restart()
	for _, m := range w.App().Initial.Crew {
		if m.Key == key {
			t.Fatalf("deleted Chief returned on restart: %+v", m)
		}
	}
}

func TestUpgradedChiefWithoutARecordedModelKeepsTheHarnessDefault(t *testing.T) {
	w := prepareChiefUpgrade(t, false, true)
	app := w.App()
	setSetting(t, app, "default_model_codex", "gpt-6.1-sol")
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && protocol.Deref(m.Session.Chief) }).Session
	run := w.Launched(string(awake.ID))
	if model, _ := flagValue(run.Argv, "--model"); model != "" {
		t.Fatalf("upgrade without a model picked %q instead of the harness default: %q", model, run.Argv)
	}
}

func TestChiefConfigurationJSONReportsSavedSettingsAndFailedWake(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude), terms: testworld.NewTerminals()}
	w.start()
	w.terms.RefuseNextSpawn(errors.New("the terminal launcher is unavailable"))
	cmd := exec.Command(testworld.AttnBinary(t), "crew", "set", "chief", "--agent", "claude", "--model", "sonnet", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = append(append(os.Environ(), w.Vars...), "ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_INSIDE_APP=")
	if err := cmd.Run(); err != nil {
		t.Fatalf("crew set: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	var result struct {
		protocol.CrewMember
		WakeError     *string             `json:"wake_error"`
		WokeSessionID *protocol.SessionID `json:"woke_session_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("member JSON: %s: %v", stdout.String(), err)
	}
	member := result.CrewMember
	if result.WokeSessionID != nil || !strings.Contains(protocol.Deref(result.WakeError), "terminal launcher is unavailable") || protocol.Deref(member.Agent) != "claude" || member.BindingSession != nil || !strings.Contains(stderr.String(), "settings saved; Chief did not start:") || !strings.Contains(stderr.String(), "terminal launcher is unavailable") {
		t.Fatalf("configured Chief: %+v stderr=%s", member, stderr.String())
	}
}

func TestUnconfiguredChiefCreationDoesNotNeedAWritableCrewFolder(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	if err := os.WriteFile(filepath.Join(w.Dir, "crew"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := createProfile(app, "Work")
	w.restart()
	side := w.AppOn(work.ID)
	if len(side.Initial.Crew) != 1 || !side.Initial.Crew[0].Chief || side.Initial.Crew[0].HomeDir != "" {
		t.Fatalf("unconfigured chief: %+v", side.Initial.Crew)
	}
}

func TestUpgradeRetriesChiefFurnishingWithoutLosingItsSettingsOrHandover(t *testing.T) {
	w := prepareChiefUpgradeAs(t, false, false, fakeagent.Codex, func(w *world) {
		if err := os.MkdirAll(filepath.Join(w.Dir, "notebook"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.Dir, "notebook", "crew"), []byte("blocked"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	app := w.App()
	if len(app.Initial.Profiles) != 1 || app.SelectedProfile() == "" {
		t.Fatalf("failed upgrade prevented startup: %+v", app.Initial.Profiles)
	}
	if err := os.Remove(filepath.Join(w.Dir, "notebook", "crew")); err != nil {
		t.Fatal(err)
	}
	w.restart()
	app, cli := w.App(), w.Client()
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := *testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && protocol.Deref(m.Session.Chief) }).Session
	run := w.Launched(string(awake.ID))
	if model, _ := flagValue(run.Argv, "--model"); model != "gpt-6.1-sol" {
		t.Fatalf("repair lost inherited model: %q", run.Argv)
	}
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	run.Prompted()
	items := readInbox(t, cli, string(awake.ID), 0)
	var notices int
	for _, item := range items.Items {
		if strings.Contains(item.Content, "You are this profile's Chief now") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("repair handover notices: %+v", items)
	}
}

func TestChiefConfigurationJSONIncludesTheWokenSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cmd := exec.Command(testworld.AttnBinary(t), "crew", "set", "chief", "--agent", "claude", "--model", "sonnet", "--json")
	cmd.Env = append(append(os.Environ(), w.Vars...), "ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_INSIDE_APP=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("crew set: %v", err)
	}
	var result struct {
		protocol.CrewMember
		WokeSessionID *protocol.SessionID `json:"woke_session_id"`
		WakeError     *string             `json:"wake_error"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("crew set JSON: %s %v", out, err)
	}
	if result.WokeSessionID == nil || result.WakeError != nil || result.BindingSession == nil || *result.BindingSession != *result.WokeSessionID {
		t.Fatalf("crew set wake result: %s", out)
	}
	testworld.AwaitSession(app, string(*result.WokeSessionID), func(s protocol.Session) bool { return protocol.Deref(s.Chief) })
}

func TestUpgradePreservesAPluginChiefBeforeItsDriverRegisters(t *testing.T) {
	t.Setenv(fakeagent.PiCapabilitiesEnv, "launch_instructions,model_pin,effort_pin,initial_prompt")
	w := prepareChiefUpgradeAs(t, false, false, fakeagent.Pi, nil)
	app := w.App()
	pluginDriverSettings(app, "pi")
	for _, member := range crewRoster(t, w.Client()) {
		if member.Chief && (protocol.Deref(member.Agent) != "pi" || protocol.Deref(member.Model) != "intent-model" || protocol.Deref(member.Effort) != "high" || member.BindingSession != nil) {
			t.Fatalf("plugin upgrade lost its configuration: %+v", member)
		}
	}
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, IdleSeconds: protocol.Ptr(0.0)})
	awake := *testworld.Await(app, protocol.EventSessionRegistered, func(m protocol.WebSocketEvent) bool { return m.Session != nil && protocol.Deref(m.Session.Chief) }).Session
	run := w.Launched(string(awake.ID))
	if model, _ := flagValue(run.Argv, "--model"); model != "intent-model" {
		t.Fatalf("plugin Chief lost its inherited model: %q", run.Argv)
	}
}
