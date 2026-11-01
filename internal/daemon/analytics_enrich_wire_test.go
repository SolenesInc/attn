package daemon_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
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
		chief := w.bubbleClaude(t, app, "notes").id
		if set := setChiefOfStaff(app, chief, true); !set.Success {
			t.Fatalf("make %s the chief: %s", chief, protocol.Deref(set.Error))
		}
		synctest.Wait()
		w.bootBubbleClaude(t, chief)
		delegation := delegateAtSeed(user.id, shop, plantDelegationSeed(t, cli, user.id, "Refund flow"))
		delegation.Agent = protocol.Ptr("claude")
		delegation.Checkout = delegateNewWorktree("refunds", "main")
		delegated, err := cli.Delegate(delegation)
		if err != nil {
			t.Fatal(err)
		}
		w.bootBubbleClaude(t, string(delegated.SessionID))
		synctest.Wait()
		setSetting(t, app, "crew_v2_enabled", "true")
		member := string(wakeCrew(t, cli, "chief", "").SessionID)
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
			{"chief", chief, "session.respawned", analytics.LaunchChief, nil},
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
			for _, text := range []string{w.Dir, "shop", "notes", "example", "Refund", user.id, chief, member, string(delegated.SessionID)} {
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

func TestAMirroredIDFieldMatchesTheSubjectOfTheSameEntity(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setAnalyticsEnabled(app, "true")
		agent := w.bubbleClaude(t, app, "planter")
		seedID := plantDelegationSeed(t, cli, agent.id, "Keyed ids")
		synctest.Wait()

		secret, err := hex.DecodeString(strings.TrimSpace(readSecret(t, filepath.Join(w.Dir, "analytics-secret"))))
		if err != nil {
			t.Fatal(err)
		}
		records := mirroredRecords(t, app, "")
		session := slices.IndexFunc(records, func(r analytics.Record) bool { return strings.HasPrefix(r.Name, "session.") && r.Subject != "" })
		planted := slices.IndexFunc(records, func(r analytics.Record) bool { return r.Name == "garden.seed.planted" })
		if session < 0 || planted < 0 {
			t.Fatalf("the outbox holds no session.* record (%d) or no garden.seed.planted record (%d)", session, planted)
		}
		if records[session].Subject != analytics.HashSubject(secret, agent.id) || records[planted].Subject != analytics.HashSubject(secret, seedID) {
			t.Fatalf("subjects %q and %q, want the hashed session and seed", records[session].Subject, records[planted].Subject)
		}
		var props struct {
			CausedBy string `json:"caused_by_session_id"`
		}
		if err := json.Unmarshal(records[planted].Props, &props); err != nil {
			t.Fatal(err)
		}
		if props.CausedBy != records[session].Subject {
			t.Errorf("garden.seed.planted carries caused_by_session_id %q in %s, want the planting session's subject %q", props.CausedBy, records[planted].Props, records[session].Subject)
		}
	})
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
		sessionFacts := func(after int64) []analytics.SessionTraits {
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
			return traits
		}
		ventModels := func() (string, string) {
			vent, err := cli.RecordVent(protocol.SessionID(id), protocol.VentKindAttn, "attn ask lost my answer.")
			if err != nil {
				t.Fatalf("vent: %v", err)
			}
			if err := cli.WithdrawVent(protocol.SessionID(id), vent.ID); err != nil {
				t.Fatalf("withdraw: %v", err)
			}
			return protocol.Deref(vent.Context.Model), protocol.Deref(vent.Context.ModelRequested)
		}

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
