package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/client"
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

type toolCall struct {
	tool string
	args map[string]any
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
	if err := os.WriteFile(filepath.Join(w.Path("shop"), "retry.log"), []byte("3 passes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, local := range []toolCall{
		{"seed_note", map[string]any{"seed": seed, "text": "-"}},
		{"seed_attach", map[string]any{"seed": seed, "path": "retry.log", "mode": "move"}},
		{"seed_detach", map[string]any{"seed": seed, "path": "retry.log", "to": "retry-back.log"}},
		{"seed_export", map[string]any{"seed": seed, "out": filepath.Join(w.Path("shop"), "seed.md")}},
		{"seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "commit": "cac9c2f"}},
		{"seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "evidence": []any{"retry.log"}}},
		{"seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "Passed", "evidence": []any{"./missing.log"}}},
	} {
		refused := agent.mustCall(t, local.tool, local.args)
		text := resultText(refused)
		if !refused.IsError || !strings.HasPrefix(text, "needs_the_cli: ") || !strings.Contains(text, "\nFix: Run `attn seed ") {
			t.Errorf("%s %v answered %q, want a needs_the_cli refusal that says to run the attn command from a shell", local.tool, local.args, text)
		}
	}
	if notes, err := w.Client().SeedNotes("", seed, client.SeedNotesQuery{Kind: "note"}); err != nil || len(notes.Notes) != 0 {
		t.Errorf("the refused calls left notes %+v, %v; want none", notes, err)
	}

	standby := agent.mustCall(t, "seed_standby", map[string]any{"seed": seed, "in": "1h"})
	if text := resultText(standby); !standby.IsError || text != "needs_next: next says what to do when mail resumes you\nFix: Pass what to do when mail resumes you in next." {
		t.Errorf("seed_standby without next answered %q, want the handler's needs_next refusal naming the arg next, with the catalog's fix", text)
	}

	unknown := agent.mustCall(t, "seed_show", map[string]any{"seed": "s-zzzzzz"})
	if text := resultText(unknown); !unknown.IsError || !strings.HasPrefix(text, "not_found: ") {
		t.Errorf("seed_show of a seed that does not exist answered %q, want a not_found refusal", text)
	}
}
