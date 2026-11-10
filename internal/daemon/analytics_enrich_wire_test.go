package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/transcript"
)

func TestAMirroredSessionFactCarriesItsSessionAndRepoTraitsAndNoFreeText(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		shop, checkout := w.Path("shop"), w.Path("shop-checkout")
		gitRepo(t, shop)
		if err := os.WriteFile(filepath.Join(shop, "AGENTS.md"), []byte("be kind\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, shop, "add", "AGENTS.md")
		runGit(t, shop, "-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-q", "-m", "guidance")
		runGit(t, shop, "remote", "add", "origin", "https://github.com/example/shop.git")
		runGit(t, shop, "worktree", "add", "-q", "-b", "checkout", checkout)
		if err := os.WriteFile(filepath.Join(checkout, "CLAUDE.md"), []byte("be brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		setAnalyticsEnabled(app, "true")

		user := w.bubbleClaude(t, app, "shop-checkout")
		delegation := delegateAtSeed(user.id, shop, plantDelegationSeed(t, cli, user.id, "Refund flow"))
		delegation.Agent = protocol.Ptr("claude")
		delegation.Checkout = delegateNewWorktree("refunds", "main")
		delegated, err := cli.Delegate(delegation)
		if err != nil {
			t.Fatal(err)
		}
		w.bootBubbleClaude(t, string(delegated.SessionID))
		synctest.Wait()
		setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		member := string(startCrew(t, cli, "chief", "").SessionID)
		w.bootBubbleClaude(t, member)
		synctest.Wait()

		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		records := mirroredRecords(t, app, "")
		created := slices.IndexFunc(records, func(r analytics.Record) bool { return r.Name == "worktree.created" })
		switch {
		case created < 0:
			t.Error("the delegation's new worktree left no worktree.created record")
		case !strings.Contains(string(records[created].Props), `"repo":{"agents_md":true,"claude_md":false,"remote":true,"worktree":true}`):
			t.Errorf("worktree.created carries %s, want the new worktree's repo traits", records[created].Props)
		}
		for _, tc := range []struct {
			name, session, fact string
			launch              analytics.Launch
			repo                *analytics.RepoTraits
		}{
			{"user", user.id, "session.state.changed", analytics.LaunchUser, &analytics.RepoTraits{AgentsMD: true, ClaudeMD: true, Worktree: true, Remote: true}},
			{"delegated", string(delegated.SessionID), "session.state.changed", analytics.LaunchDelegation, &analytics.RepoTraits{AgentsMD: true, Worktree: true, Remote: true}},
			{"chief member", member, "session.state.changed", analytics.LaunchChief, nil},
		} {
			subject := analytics.HashSubject(secret, tc.session)
			latest := slices.IndexFunc(records, func(r analytics.Record) bool { return r.Name == tc.fact && r.Subject == subject })
			if latest < 0 {
				t.Errorf("no %s record for the %s session", tc.fact, tc.name)
				continue
			}
			props := records[latest].Props
			var got analytics.Derived
			if err := json.Unmarshal(props, &got); err != nil {
				t.Fatalf("%s props %s: %v", tc.name, props, err)
			}
			if got.Session == nil || got.Session.Harness != protocol.SessionAgentClaude || got.Session.Launch != tc.launch || got.Session.State == "" {
				t.Errorf("the %s session's latest %s carries %s, want harness claude, launch %s and a state", tc.name, tc.fact, props, tc.launch)
			}
			if (got.Repo == nil) != (tc.repo == nil) || (got.Repo != nil && *got.Repo != *tc.repo) {
				t.Errorf("the %s session's latest %s carries %s, want repo traits %+v", tc.name, tc.fact, props, tc.repo)
			}
		}

		for _, record := range records {
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{w.Dir, "shop", "example", "Refund", user.id, member, string(delegated.SessionID)} {
				if strings.Contains(string(encoded), text) {
					t.Errorf("mirrored %s carries %q: %s", record.Name, text, encoded)
				}
			}
			assertDerivedTraitsAreClosed(t, record)
		}
	})
}

func assertDerivedTraitsAreClosed(t *testing.T, record analytics.Record) {
	t.Helper()
	if len(record.Props) == 0 {
		return
	}
	var props struct {
		Session map[string]any `json:"session"`
		Repo    map[string]any `json:"repo"`
	}
	if err := json.Unmarshal(record.Props, &props); err != nil {
		t.Fatalf("%s props %s: %v", record.Name, record.Props, err)
	}
	closed := map[string]map[string]bool{
		"harness":         {protocol.SessionAgentClaude: true},
		"model":           {"fable": true},
		"model_requested": {"fable": true},
		"launch":          {string(analytics.LaunchUser): true, string(analytics.LaunchDelegation): true, string(analytics.LaunchAutomation): true, string(analytics.LaunchCrew): true, string(analytics.LaunchChief): true},
		"state":           {},
	}
	for _, state := range []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateLaunching, protocol.SessionStatePendingApproval, protocol.SessionStateRecoverable, protocol.SessionStateScheduled, protocol.SessionStateUnknown, protocol.SessionStateWaitingInput, protocol.SessionStateWorking} {
		closed["state"][string(state)] = true
	}
	for key, value := range props.Session {
		text, isText := value.(string)
		if !isText || !closed[key][text] {
			t.Errorf("%s session.%s = %v, want one of %v", record.Name, key, value, closed[key])
		}
	}
	for key, value := range props.Repo {
		if _, isBool := value.(bool); !isBool {
			t.Errorf("%s repo.%s = %v, want a boolean", record.Name, key, value)
		}
	}
}

func mirroredRecords(t *testing.T, app *testworld.Peer, name string) []analytics.Record {
	t.Helper()
	listed := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000, Stream: protocol.Ptr("bus")})
	var records []analytics.Record
	for _, row := range listed.Records {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		if name == "" || record.Name == name {
			records = append(records, record)
		}
	}
	return records
}

func TestAMirroredSettingChangeNamesTheSettingAndNotItsConcreteKey(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		setSetting(t, app, "new_session_destination_local_/Users/someone/shop", "main_repo")
		synctest.Wait()

		var subjects []string
		for _, record := range mirroredRecords(t, app, "setting.changed") {
			subjects = append(subjects, record.Subject)
		}
		if !slices.Contains(subjects, "analytics.enabled") || !slices.Contains(subjects, "new_session_destination_<repository>") {
			t.Errorf("setting.changed subjects %v, want analytics.enabled and the pattern new_session_destination_<repository>", subjects)
		}
		for _, subject := range subjects {
			if strings.Contains(subject, "someone") {
				t.Errorf("setting.changed subject %q carries the concrete key's path", subject)
			}
		}
	})
}

func TestSessionFactsAndVentsTellTheReportedModelFromTheRequestedOne(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setAnalyticsEnabled(app, "true")
		id := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Model = protocol.Ptr("opus") })
		agent := w.bootBubbleClaude(t, id)
		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		subject := analytics.HashSubject(secret, id)
		sessionFacts := func(after int64) []analytics.SessionTraits { return mirroredSessionTraits(t, app, subject, after) }
		ventModels := func() (string, string) { return ventModelsOf(t, cli, id) }

		before := sessionFacts(0)
		if len(before) == 0 {
			t.Fatal("the outbox holds no session fact before usage")
		}
		for _, traits := range before {
			if traits.Model != "" || traits.ModelRequested != "opus" {
				t.Errorf("before usage a session fact carries model %q and model_requested %q, want no model and model_requested opus", traits.Model, traits.ModelRequested)
			}
		}
		if model, requested := ventModels(); model != "" || requested != "opus" {
			t.Errorf("before usage a vent carries model %q and model_requested %q, want no model and model_requested opus", model, requested)
		}

		var last int64
		for _, record := range mirroredRecords(t, app, "") {
			last = max(last, record.Seq)
		}
		appendLegacyCostUsage(t, agent.transcript.Path, "reply-1", "claude-opus-5-5", 10, 4)
		w.advance(time.Second)
		awaitUsageTokens(app, id, 14)
		app.TypeLine(id, "ship the refund flow")
		agent.reply("shipped <!-- attn:state=idle -->")

		after := sessionFacts(last)
		if len(after) == 0 {
			t.Fatal("the outbox holds no session fact after usage")
		}
		if latest := after[len(after)-1]; latest.Model != "claude-opus-5-5" || latest.ModelRequested != "opus" {
			t.Errorf("after usage the latest session fact carries model %q and model_requested %q, want claude-opus-5-5 and opus", latest.Model, latest.ModelRequested)
		}
		if model, requested := ventModels(); model != "claude-opus-5-5" || requested != "opus" {
			t.Errorf("after usage a vent carries model %q and model_requested %q, want claude-opus-5-5 and opus", model, requested)
		}
	})
}

func mirroredSessionTraits(t *testing.T, app *testworld.Peer, subject string, after int64) []analytics.SessionTraits {
	t.Helper()
	var traits []analytics.SessionTraits
	for _, record := range mirroredRecords(t, app, "") {
		if record.Subject != subject || !strings.HasPrefix(record.Name, "session.") || record.Seq <= after {
			continue
		}
		var derived analytics.Derived
		if err := json.Unmarshal(record.Props, &derived); err != nil {
			t.Fatalf("%s props %s: %v", record.Name, record.Props, err)
		}
		if derived.Session != nil {
			traits = append(traits, *derived.Session)
		}
	}
	slices.Reverse(traits)
	return traits
}

func ventModelsOf(t *testing.T, cli *client.Client, id string) (string, string) {
	t.Helper()
	vent, err := cli.RecordVent(protocol.SessionID(id), protocol.VentKindAttn, "attn ask lost my answer.")
	if err != nil {
		t.Fatalf("vent: %v", err)
	}
	if err := cli.WithdrawVent(protocol.SessionID(id), vent.ID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	return protocol.Deref(vent.Context.Model), protocol.Deref(vent.Context.ModelRequested)
}

func TestSessionFactsAndVentsNameTheMainLoopsModelWhenASubagentUsesMoreTokensOnAnother(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setAnalyticsEnabled(app, "true")
		id := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Model = protocol.Ptr("opus") })
		agent := w.bootBubbleClaude(t, id)
		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		subject := analytics.HashSubject(secret, id)
		subagent := filepath.Join(strings.TrimSuffix(agent.transcript.Path, ".jsonl"), "subagents", "agent-research.jsonl")
		if err := os.MkdirAll(filepath.Dir(subagent), 0o755); err != nil {
			t.Fatal(err)
		}
		usageAfterATick := func(tokens int) *protocol.SessionUsage {
			t.Helper()
			w.advance(time.Second)
			usage := queriedSession(t, cli, id).Usage
			if usage == nil || usage.TotalTokens != tokens {
				t.Fatalf("usage = %+v, want %d tokens", usage, tokens)
			}
			return usage
		}
		expect := func(moment, wantModel string) {
			t.Helper()
			facts := mirroredSessionTraits(t, app, subject, 0)
			if len(facts) == 0 {
				t.Fatalf("%s: the outbox holds no session fact", moment)
			}
			if latest := facts[len(facts)-1]; latest.Model != wantModel || latest.ModelRequested != "opus" {
				t.Errorf("%s: the latest session fact carries model %q and model_requested %q, want %q and opus", moment, latest.Model, latest.ModelRequested, wantModel)
			}
			if model, requested := ventModelsOf(t, cli, id); model != wantModel || requested != "opus" {
				t.Errorf("%s: a vent carries model %q and model_requested %q, want %q and opus", moment, model, requested, wantModel)
			}
		}

		appendLegacyCostUsage(t, subagent, "research-1", "claude-sonnet-5-5", 1000, 1000)
		appendLegacyCostUsage(t, agent.transcript.Path, "root-no-usage-1", "claude-opus-5-5", 0, 0)
		usageAfterATick(2000)
		expect("with only subagent usage", "")

		appendLegacyCostUsage(t, agent.transcript.Path, "root-1", "claude-opus-5-5", 10, 4)
		usageAfterATick(2014)
		expect("with root usage smaller than the subagent's", "claude-opus-5-5")

		appendLegacyCostUsage(t, subagent, "research-2", "claude-sonnet-5-5", 5000, 5000)
		appendLegacyCostUsage(t, agent.transcript.Path, "root-no-usage-2", "claude-opus-5-5", 0, 0)
		usage := usageAfterATick(12014)
		expect("after the subagent's usage grew", "claude-opus-5-5")
		rows := map[string]int{}
		for _, row := range usage.Models {
			if row.Purpose != "agent" {
				t.Errorf("usage row %+v, want purpose agent", row)
			}
			rows[row.Model] = row.TotalTokens
		}
		if rows["claude-sonnet-5-5"] != 12000 || rows["claude-opus-5-5"] != 14 {
			t.Errorf("usage rows %v, want the subagent's 12000 tokens under its model beside the root's 14", rows)
		}

		w.restart()
		if model, _ := ventModelsOf(t, w.Client(), id); model != "claude-opus-5-5" {
			t.Errorf("after a restart a vent carries model %q, want claude-opus-5-5", model)
		}
	})
}

func TestASessionSavedBeforeRootsWereRecordedGetsItsMainLoopsModelBack(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "old work")
		agent.reply("done <!-- attn:state=idle -->")
		cursor, err := transcript.HeadCursor(agent.transcript.Path)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := json.Marshal(map[string]any{"initialized": true, "sources": map[string]any{agent.transcript.Path: map[string]any{
			"initialized": true, "cursor": cursor, "ledger": map[string]any{"agent|claude-opus-4-8": map[string]any{"input_tokens": 10, "output_tokens": 2}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		w.stop()
		loadLegacyCostFixture(t, w, agent.id, string(saved))
		w.start()
		w.advance(time.Second)
		if model, _ := ventModelsOf(t, w.Client(), agent.id); model != "claude-opus-4-8" {
			t.Errorf("a vent carries model %q, want claude-opus-4-8", model)
		}
	})
}

func TestACodexSessionsFactsNameItsRootRolloutsModelOverASubagents(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setAnalyticsEnabled(app, "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	codex.CodexSettings("gpt-6.1-sol", "default")
	app.TypeLine(session, "continue")
	codex.Prompted()
	reply := "Ready. <!-- attn:state=idle -->"
	mark := len(app.Log())
	codex.Reply(reply)
	tokens := 2 * len(reply)
	awaitUsageTokens(app, session, tokens)
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	codex.CodexSettings("gpt-5.5", "default")
	research := strings.Repeat("The refund flow reads the ledger. ", 10)
	codex.Subagent(research)
	tokens += 2 * len(research)
	awaitUsageTokens(app, session, tokens)
	if model, _ := ventModelsOf(t, w.Client(), session); model != "gpt-6.1-sol" {
		t.Errorf("a vent carries model %q, want the root rollout's gpt-6.1-sol", model)
	}
}

func TestMirroredActorAndTerminalIDsJoinToTheSessionsAndTerminalsTheyName(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setAnalyticsEnabled(app, "true")
		setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		chief := string(startCrew(t, cli, "chief", "").SessionID)
		w.bootBubbleClaude(t, chief)
		target := w.bubbleClaude(t, app, "target")
		closedByUser := w.bubbleClaude(t, app, "closed-by-user")
		first := plantDelegationSeed(t, cli, chief, "First")
		second := plantDelegationSeed(t, cli, chief, "Second")
		scoped := cli.WithGardenProfile("", protocol.SessionID(chief))
		if _, err := scoped.SeedLink(first, "blocks", second, false); err != nil {
			t.Fatal(err)
		}
		if _, err := scoped.SeedLink(first, "blocks", second, true); err != nil {
			t.Fatal(err)
		}
		if result, err := cli.AgentReload([]string{target.id}, protocol.SessionID(chief), false); err != nil || len(result.Outcomes) != 1 || !result.Outcomes[0].Success {
			t.Fatalf("reload = %+v, %v", result, err)
		}
		synctest.Wait()
		if _, err := cli.AgentClose(target.id, protocol.SessionID(chief), "finished"); err != nil {
			t.Fatalf("the chief closes the target: %v", err)
		}
		if closed := closeFromApp(app, closedByUser.id); !closed.Accepted {
			t.Fatalf("the user closes a session: %+v", closed)
		}
		synctest.Wait()

		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		chiefSubject, terminal := analytics.HashSubject(secret, chief), analytics.HashSubject(secret, target.self)
		for _, want := range []struct {
			name, subject, field, value string
		}{
			{"garden.seed.edge.unlinked", analytics.HashSubject(secret, first), "caused_by_session_id", chiefSubject},
			{"session.respawned", analytics.HashSubject(secret, target.id), "terminal", terminal},
			{"session.pty.exited", analytics.HashSubject(secret, target.id), "terminal", terminal},
			{"session.closed", analytics.HashSubject(secret, target.id), "closed_by", chiefSubject},
			{"session.closed", analytics.HashSubject(secret, closedByUser.id), "closed_by", "user"},
		} {
			var values []string
			for _, record := range mirroredRecords(t, app, want.name) {
				if record.Subject != want.subject {
					continue
				}
				var props map[string]any
				if err := json.Unmarshal(record.Props, &props); err != nil {
					t.Fatal(err)
				}
				values = append(values, fmt.Sprint(props[want.field]))
			}
			if !slices.Contains(values, want.value) {
				t.Errorf("%s about %s carries %s %v, want %q", want.name, want.subject, want.field, values, want.value)
			}
		}
	})
}

func TestAMirroredSessionUnregisteredCarriesWhyItsStateAndTerminalEnded(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setAnalyticsEnabled(app, "true")
		agent := w.bubbleClaude(t, app, "shop")
		w.terminal(agent.id).ExitBySignal(syscall.SIGKILL)
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == agent.id })
		desktop, tile := tileOf(t, w, app, w.Terminal(agent.id))
		if closed := closeTileFromApp(app, desktop, tile); !closed.Success {
			t.Fatalf("close the killed agent's tile: %s", protocol.Deref(closed.Error))
		}
		synctest.Wait()

		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		subject := analytics.HashSubject(secret, agent.id)
		records := mirroredRecords(t, app, "session.unregistered")
		at := slices.IndexFunc(records, func(r analytics.Record) bool { return r.Subject == subject })
		if at < 0 {
			t.Fatalf("no session.unregistered record for the killed agent among %d", len(records))
		}
		props := records[at].Props
		var got struct {
			StateReason  string `json:"state_reason"`
			TerminalExit struct {
				Code   int    `json:"code"`
				Signal string `json:"signal"`
			} `json:"terminal_exit"`
		}
		if err := json.Unmarshal(props, &got); err != nil {
			t.Fatalf("props %s: %v", props, err)
		}
		if got.StateReason != "process_exited" || got.TerminalExit.Signal != "killed" || got.TerminalExit.Code != -1 {
			t.Errorf("session.unregistered carries %s, want state_reason process_exited and terminal_exit signal killed", props)
		}
		var shape map[string]any
		if err := json.Unmarshal(props, &shape); err != nil {
			t.Fatal(err)
		}
		delete(shape, "session")
		delete(shape, "repo")
		texts := map[string]bool{}
		collectTextPaths("", shape, texts)
		for path := range texts {
			if _, classified := analytics.BusFields["session.unregistered"][path]; !classified {
				t.Errorf("session.unregistered ships %s as text, want its length: %s", path, props)
			}
		}
		if strings.Contains(string(props), w.Dir) {
			t.Errorf("session.unregistered carries the session's directory: %s", props)
		}
	})
}

func collectTextPaths(prefix string, value any, into map[string]bool) {
	switch v := value.(type) {
	case string:
		into[prefix] = true
	case []any:
		for _, item := range v {
			collectTextPaths(prefix, item, into)
		}
	case map[string]any:
		for key, item := range v {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			collectTextPaths(path, item, into)
		}
	}
}
