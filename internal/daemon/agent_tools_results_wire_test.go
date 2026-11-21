package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type offeredTools map[string]*jsonschema.Resolved

func listOfferedTools(t *testing.T, agent agentToolsCaller) offeredTools {
	t.Helper()
	session, err := agent.connect(t, "")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := offeredTools{}
	for _, tool := range listed.Tools {
		if tool.OutputSchema == nil {
			tools[tool.Name] = nil
			continue
		}
		data, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: outputSchema: %v", tool.Name, err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatalf("%s: outputSchema does not resolve: %v", tool.Name, err)
		}
		tools[tool.Name] = resolved
	}
	return tools
}

func TestEverySeedToolAnswersWithTheShapeItsOutputSchemaDescribes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)

	called := map[string]bool{}
	var mismatches []string
	call := func(tool string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		called[tool] = true
		result := agent.mustCall(t, tool, args)
		if result.IsError {
			mismatches = append(mismatches, tool+": refused: "+resultText(result))
			return result
		}
		schema, ok := tools[tool]
		if !ok {
			t.Fatalf("%s is not offered", tool)
		}
		if schema == nil {
			return result
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var instance any
		if err := json.Unmarshal(data, &instance); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(instance); err != nil {
			mismatches = append(mismatches, tool+": "+err.Error())
		}
		return result
	}
	seedOf := func(r *mcp.CallToolResult) string {
		t.Helper()
		var shape struct {
			ID   string `json:"id"`
			Seed struct {
				ID string `json:"id"`
			} `json:"seed"`
		}
		data, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(data, &shape)
		if shape.ID != "" {
			return shape.ID
		}
		return shape.Seed.ID
	}

	x := seedOf(call("seed_plant", map[string]any{"title": "Fix the flaky checkout test", "body": "Retry the payment call."}))
	y := seedOf(call("seed_plant", map[string]any{"title": "Ship the retry", "body": "After the fix."}))
	z := seedOf(call("seed_plant", map[string]any{"title": "Document the retry", "body": "Say why."}))
	if x == "" || y == "" || z == "" {
		t.Fatalf("seed_plant answered no seed ids: %q %q %q; %v", x, y, z, mismatches)
	}
	call("seed_plot", map[string]any{"plot": map[string]any{"title": "Checkout plot", "children": []any{map[string]any{"title": "Step one"}}}})
	call("seed_search", map[string]any{"words": "flaky checkout"})
	call("seed_link", map[string]any{"a": x, "relation": "blocks", "b": y})
	call("seed_unlink", map[string]any{"a": x, "relation": "blocks", "b": y})
	call("seed_ready", map[string]any{})
	call("seed_ls", map[string]any{})
	call("seed_show", map[string]any{"seed": x})
	call("seed_notes", map[string]any{"seed": x})
	call("seed_prime", map[string]any{})
	call("seed_guide", map[string]any{})
	call("seed_tend", map[string]any{"seed": x})
	call("seed_note", map[string]any{"seed": x, "text": "Retries pass locally."})
	call("seed_check_add", map[string]any{"seed": x, "statement": "C1=Retries pass in CI"})
	call("seed_check_edit", map[string]any{"seed": x, "id": "C1", "statement": "Retries pass in CI twice"})
	call("seed_check_report", map[string]any{"seed": x, "id": "C1", "summary": "Passed twice"})
	call("seed_check_reject", map[string]any{"seed": x, "id": "C1", "proof": "Run it a third time"})
	call("seed_check_report", map[string]any{"seed": x, "id": "C1", "summary": "Passed three times"})
	call("seed_check_confirm", map[string]any{"seed": x, "id": "C1"})
	call("seed_check_uncheck", map[string]any{"seed": x, "id": "C1"})
	call("seed_check_reopen", map[string]any{"seed": x, "id": "C1"})
	call("seed_check_remove", map[string]any{"seed": x, "id": "C1", "reason": "Folded into C2"})
	call("seed_check_restore", map[string]any{"seed": x, "id": "C1"})
	call("seed_watch", map[string]any{"seed": y})
	call("seed_unwatch", map[string]any{"seed": y})
	call("seed_remind", map[string]any{"seed": x, "in": "1h", "message": "Check CI"})
	call("seed_standby", map[string]any{"seed": x, "in": "1h", "next": "Read the CI result.", "stay": true})
	call("seed_authorization", map[string]any{"seed": x, "words": "Ask before pushing."})

	var exported struct {
		Text string `json:"text"`
	}
	if data, err := json.Marshal(call("seed_export", map[string]any{"seed": x}).StructuredContent); err == nil {
		_ = json.Unmarshal(data, &exported)
	}
	call("seed_edit", map[string]any{"seed": x, "body": exported.Text + "\nAlso retry refunds.\n"})
	call("seed_rollback", map[string]any{"seed": x})

	if err := os.WriteFile(filepath.Join(w.Path("shop"), "retry.log"), []byte("3 passes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call("seed_attach", map[string]any{"seed": x, "path": filepath.Join(w.Path("shop"), "retry.log"), "mode": "move"})
	call("seed_detach", map[string]any{"seed": x, "path": "retry.log", "to": filepath.Join(w.Path("shop"), "retry-back.log")})

	call("seed_park", map[string]any{"seed": x, "reason": "Waiting on CI"})
	call("seed_wither", map[string]any{"seed": y, "reason": "Superseded"})
	call("seed_replant", map[string]any{"seed": y})
	call("seed_harvest", map[string]any{"seed": z, "reason": "Documented"})
	call("seed_send_to_chief", map[string]any{"seed": y, "guidance": "Pick a builder"})

	abandoned := gardenReviewAbandonedSeed(t, w, app, w.Client(), "gardener", "old work")
	gardenReviewAbandonedSeed(t, w, app, w.Client(), "planter", "older work")
	review := call("seed_review_start", map[string]any{})
	var started struct {
		Review struct {
			Run struct {
				ID string `json:"id"`
			} `json:"run"`
		} `json:"review"`
	}
	if data, err := json.Marshal(review.StructuredContent); err == nil {
		_ = json.Unmarshal(data, &started)
	}
	call("seed_review_show", map[string]any{"review": started.Review.Run.ID})
	everyItem := func(want func(protocol.GardenReviewItem) bool) {
		t.Helper()
		testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
			return m.Review.Run.ID == started.Review.Run.ID && !slices.ContainsFunc(m.Review.Items, func(item protocol.GardenReviewItem) bool { return !want(item) })
		})
	}
	w.advance(0)
	for attempt, wait := range []time.Duration{time.Minute, 2 * time.Minute} {
		everyItem(func(item protocol.GardenReviewItem) bool {
			return protocol.Deref(item.AdvisorState) == "retrying" && protocol.Deref(item.AdvisorAttempt) == attempt+1
		})
		w.advance(wait)
	}
	everyItem(func(item protocol.GardenReviewItem) bool { return item.Status == "failed" })
	if _, err := editSeedBody(t, w.Client(), abandoned, "A newer body for the retry."); err != nil {
		t.Fatal(err)
	}
	call("seed_review_retry", map[string]any{"review": started.Review.Run.ID, "seed": abandoned})
	call("seed_review_keep", map[string]any{"review": started.Review.Run.ID, "seed": abandoned})
	call("seed_review_cancel", map[string]any{"review": started.Review.Run.ID})

	var uncalled []string
	for tool := range tools {
		if strings.HasPrefix(tool, "seed_") && !called[tool] {
			uncalled = append(uncalled, tool)
		}
	}
	sort.Strings(uncalled)
	if len(uncalled) > 0 {
		t.Errorf("offered seed tools this test never called: %v", uncalled)
	}
	for _, mismatch := range mismatches {
		t.Error(mismatch)
	}
}

func TestEverySeedToolRefusesWithACodeAndAFix(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	for tool := range listOfferedTools(t, agent) {
		refused := agent.mustCall(t, tool, map[string]any{"session": "someone-else"})
		text := resultText(refused)
		code, rest, _ := strings.Cut(text, ": ")
		if !refused.IsError || code != "usage" || !strings.Contains(rest, `"session"`) || !strings.Contains(rest, "\nFix: Call "+tool+" with only the arguments") {
			t.Errorf("%s with an argument it does not take answered %q, want a usage refusal naming the argument, with a fix", tool, text)
		}
	}
	seed := plantForTools(t, w)
	standby := agent.mustCall(t, "seed_standby", map[string]any{"seed": seed, "in": "1h"})
	if text := resultText(standby); !standby.IsError || text != "needs_next: next says what to do when mail resumes you\nFix: Pass what to do when mail resumes you in next." {
		t.Errorf("seed_standby without next answered %q, want the handler's needs_next refusal naming the arg next, with the catalog's fix", text)
	}

	unknown := agent.mustCall(t, "seed_show", map[string]any{"seed": "s-zzzzzz"})
	if text := resultText(unknown); !unknown.IsError || !strings.HasPrefix(text, "not_found: ") {
		t.Errorf("seed_show of a seed that does not exist answered %q, want a not_found refusal", text)
	}
}

func TestAToolCallDoesTheCallersWorkInTheFolderTheSessionStartedIn(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	shop := w.Path("shop")
	gitRepo(t, shop)
	session := w.Spawn(app, fakeagent.Claude, shop)
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	seed := plantForTools(t, w)
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(shop, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	structured := func(tool string, args map[string]any, into any) {
		t.Helper()
		result := agent.mustCall(t, tool, args)
		if result.IsError {
			t.Fatalf("%s %v refused: %s", tool, args, resultText(result))
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	head, err := exec.Command("git", "-C", shop, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	full := strings.TrimSpace(string(head))

	write("plan.md", "the plan\n")
	var attached protocol.SeedArtifactResult
	structured("seed_attach", map[string]any{"seed": seed, "path": "plan.md", "mode": "move"}, &attached)
	if protocol.Deref(attached.SourcePath) != filepath.Join(shop, "plan.md") {
		t.Errorf("seed_attach of plan.md moved %q, want %s, the file in the session's folder", protocol.Deref(attached.SourcePath), filepath.Join(shop, "plan.md"))
	}
	if _, err := os.Stat(filepath.Join(shop, "plan.md")); !os.IsNotExist(err) {
		t.Errorf("plan.md is still in the session's folder after a move: %v", err)
	}
	var retried protocol.SeedArtifactResult
	structured("seed_attach", map[string]any{"seed": seed, "path": "plan.md", "mode": "move"}, &retried)
	if protocol.Deref(retried.OperationID) != protocol.Deref(attached.OperationID) || !protocol.Deref(retried.Recovered) {
		t.Errorf("seed_attach of plan.md again after the move answered operation %q recovered %v, want the first call's operation %q recovered", protocol.Deref(retried.OperationID), protocol.Deref(retried.Recovered), protocol.Deref(attached.OperationID))
	}

	var detached protocol.SeedArtifactResult
	structured("seed_detach", map[string]any{"seed": seed, "path": "plan.md", "to": "plan-back.md"}, &detached)
	if got, err := os.ReadFile(filepath.Join(shop, "plan-back.md")); err != nil || string(got) != "the plan\n" {
		t.Errorf("seed_detach to plan-back.md left %q, %v in the session's folder; want the plan", got, err)
	}

	structured("seed_check_add", map[string]any{"seed": seed, "statement": "C1=Retries pass"}, &struct{}{})
	write("retry.log", "3 passes\n")
	var reported protocol.SeedCheckResult
	structured("seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "evidence": []any{"retry.log", "https://example.com/run"}, "commit": full[:7]}, &reported)
	if len(reported.Attached) != 1 || reported.Attached[0].Filename != "retry.log" {
		t.Errorf("seed_check_report attached %+v, want retry.log copied from the session's folder", reported.Attached)
	}
	for _, check := range reported.Seed.Checks {
		if check.ID != "C1" {
			continue
		}
		if check.Report == nil || protocol.Deref(check.Report.Commit) != full {
			t.Errorf("C1's report is %+v, want commit %s, the short SHA resolved in the session's repository", check.Report, full)
		}
	}

	var exported protocol.SeedExportResult
	structured("seed_export", map[string]any{"seed": seed}, &exported)
	var edited protocol.SeedEditResult
	structured("seed_edit", map[string]any{"seed": seed, "body": protocol.Deref(exported.Text) + "Also retry refunds.\n"}, &edited)
	if shown, err := w.Client().SeedShow("", seed); err != nil || !strings.HasSuffix(shown.Seed.Body, "Also retry refunds.\n") {
		t.Errorf("seed_edit of the exported text left the body %q, %v; want the edit saved", shown.Seed.Body, err)
	}

	for _, refused := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"seed_attach", map[string]any{"seed": seed, "path": "gone.md", "mode": "copy"}, `"gone.md" is not found in ` + shop + `, the folder the session started in; pass an absolute path`},
		{"seed_detach", map[string]any{"seed": seed, "path": "retry.log", "to": "nowhere/retry.log"}, `"nowhere/retry.log" is not found in ` + shop},
		{"seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "evidence": []any{"./missing.log"}}, "./missing.log is not found in " + shop},
		{"seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "commit": "no-such-rev"}, `commit "no-such-rev" is not a commit in the repository of ` + shop},
		{"seed_edit", map[string]any{"seed": seed, "body": "A body without its stamp line"}, "not_stamped: edit takes an exported file"},
	} {
		result := agent.mustCall(t, refused.tool, refused.args)
		if text := resultText(result); !result.IsError || !strings.Contains(text, refused.want) {
			t.Errorf("%s %v answered %q, want a refusal containing %q", refused.tool, refused.args, text, refused.want)
		}
	}
}
