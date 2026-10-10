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
	charter, err := os.ReadFile(chief.CharterPath)
	if err != nil || string(charter) != prompts.RenderText("chief", "charter", nil) {
		t.Fatalf("chief charter %q: %v", charter, err)
	}
	work := createProfile(app, "Work")
	side := w.AppOn(work.ID)
	var workChief protocol.CrewMember
	for _, m := range side.Initial.Crew {
		if m.Chief {
			workChief = m
		}
	}
	if workChief.Key == "" || workChief.Key == chief.Key {
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
	for _, change := range []protocol.CrewSetMessage{
		{Agent: protocol.Ptr("")},
		{Agent: protocol.Ptr("copilot"), Model: protocol.Ptr("test-model")},
		{Agent: protocol.Ptr("claude")},
	} {
		_, err := cli.CrewSet("chief", nil, change.Agent, change.Model, nil, nil)
		if err == nil {
			t.Fatalf("Chief accepted configuration %+v", change)
		}
	}
	id := configureChiefOn(t, w, app, fakeagent.Claude, "sonnet")
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
	for key, want := range map[string]string{"claude_cap_launch_instructions": "true", "codex_cap_launch_instructions": "true", "copilot_cap_launch_instructions": "false"} {
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
	if _, err := cli.CrewRename("chief", "Alfred"); err != nil {
		t.Fatal(err)
	}
	slept, err := cli.CrewHandoff(protocol.SessionID(id), "Return when mail arrives.", false, protocol.CrewDayCloseSleep)
	if err != nil || protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep {
		t.Fatalf("sleep: %+v %v", slept, err)
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
	if _, err := cli.CrewRename("chief", "Chief"); err != nil {
		t.Fatal(err)
	}
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
		handoff, err := w.Client().CrewHandoff(protocol.SessionID(id), "A fresh Chief session should continue.", false, "")
		if err != nil || protocol.Deref(handoff.Outcome) != protocol.CrewDayCloseNap || handoff.SessionID == nil {
			t.Fatalf("plain Chief handoff: %+v %v", handoff, err)
		}
	}, fakeagent.Claude)
}

func prepareChiefUpgrade(t *testing.T, missingCWD, emptyModel bool, bound ...bool) *world {
	t.Helper()
	w := &world{World: prepareWorld(t, fakeagent.Codex)}
	cwd := w.Path("old-chief")
	if !missingCWD {
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.OpenDBAtSchemaVersion(filepath.Join(w.Dir, "attn.db"), 1791645298598165-1)
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
		{"INSERT INTO sessions(id,label,directory,state,state_since,state_updated_at,last_seen,agent,agent_metadata,launch_intent,profile_id,launched_at) VALUES('previous-chief','Chief',?,'idle',?,?,?,'codex','recorded-conversation',?, ?,?)", []any{cwd, stamp, stamp, stamp, intent, profile, stamp}},
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
		if _, err := db.Exec("UPDATE sessions SET member_key='keel' WHERE id='previous-chief'"); err != nil {
			t.Fatal(err)
		}
		historicDocument(crew.Namespace, crew.CollectionMembers, "keel", map[string]any{"home_dir": home, "charter_path": filepath.Join(home, "CHARTER.md"), "cwd": cwd, "agent": "codex", "binding_session": "previous-chief"})
	}
	historicDocument(garden.Namespace, garden.CollectionSeeds, "s-claim1", map[string]any{"id": "s-claim1", "profile_id": profile, "title": "Inherited work", "body": "Finish this work.", "status": "growing", "tender": previousParty, "state_changed_at": stamp})
	if _, err := db.Exec("INSERT INTO garden_seed_watches(watcher,seed_id,created_at) VALUES(?, 's-claim1', ?)", previousParty, stamp); err != nil {
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
	if protocol.Deref(chief.Agent) != "codex" || protocol.Deref(chief.Model) != "gpt-6.1-sol" || protocol.Deref(chief.Effort) != "high" || protocol.Deref(chief.Cwd) != w.Path("old-chief") || chief.BindingSession != nil {
		t.Fatalf("upgraded Chief before presence: %+v", chief)
	}
	doc, err := cli.DocGet(crew.Namespace, crew.CollectionMembers, chief.Key)
	if err != nil || doc.Document == nil || !strings.Contains(doc.Document.Body, `"onboarded":true`) {
		t.Fatalf("upgraded marker: %+v %v", doc, err)
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
	if !strings.Contains(content, "Tended: s-claim1") || !strings.Contains(content, "Watched: s-claim1") || !strings.Contains(content, "previous-chief") || !strings.Contains(content, "attn session transcript") || !strings.Contains(content, "Saved before the upgrade") {
		t.Fatalf("handover mail: %s", content)
	}
}

func TestUpgradeHandoverIncludesTheOldChiefsMemberOwnedWork(t *testing.T) {
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
		if strings.Contains(item.Content, "Take over from it") {
			handover = item.Content
		}
	}
	if !strings.Contains(handover, "Tended: s-claim1") || !strings.Contains(handover, "Watched: s-claim1") {
		t.Fatalf("member-owned handover: %s", handover)
	}
}

func TestUpgradePreservesAnUnavailableChiefDirectoryWithoutBlockingTheDaemon(t *testing.T) {
	w := prepareChiefUpgrade(t, true, false)
	var chief protocol.CrewMember
	for _, m := range w.App().Initial.Crew {
		if m.Chief {
			chief = m
		}
	}
	if protocol.Deref(chief.Cwd) != w.Path("old-chief") || chief.BindingSession != nil {
		t.Fatalf("missing inherited directory changed Chief: %+v", chief)
	}
	_, err := w.Client().CrewWake("chief", "", "")
	crewErrorContains(t, err, "is not there", "attn crew set")
}

func TestChiefOneDayHarnessOverrideKeepsTheMembersPinsOnTheirHarness(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	id := configureChiefOn(t, w, app, fakeagent.Claude, "opus")
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	if _, err := cli.CrewHandoff(protocol.SessionID(id), "Rest until the next request.", false, protocol.CrewDayCloseSleep); err != nil {
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
	if _, err := w.Client().CrewHandoff(protocol.SessionID(id), "The profile is being removed.", false, protocol.CrewDayCloseSleep); err != nil {
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
	var member protocol.CrewMember
	if err := json.Unmarshal(stdout.Bytes(), &member); err != nil {
		t.Fatalf("member JSON: %s: %v", stdout.String(), err)
	}
	if protocol.Deref(member.Agent) != "claude" || member.BindingSession != nil || !strings.Contains(stderr.String(), "settings saved; Chief did not start:") || !strings.Contains(stderr.String(), "terminal launcher is unavailable") {
		t.Fatalf("configured Chief: %+v stderr=%s", member, stderr.String())
	}
}
