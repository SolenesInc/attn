package daemon_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestEverySeedCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	setSetting(t, app, "garden_v2_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	tools := listOfferedTools(t, agent)

	shapes := newToolShapes(t, agent, tools)
	call := shapes.call
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
		t.Fatalf("seed_plant answered no seed ids: %q %q %q; %v", x, y, z, shapes.mismatches)
	}
	call("seed_plot", map[string]any{"plot": map[string]any{"title": "Checkout plot", "children": []any{map[string]any{"title": "Step one"}}}})
	call("seed_search", map[string]any{"query": "flaky checkout"})
	call("seed_link", map[string]any{"a": x, "relation": "blocks", "b": y})
	call("seed_unlink", map[string]any{"a": x, "relation": "blocks", "b": y})
	call("seed_ready", map[string]any{})
	call("seed_ls", map[string]any{})
	call("seed_show", map[string]any{"seed": x})
	call("seed_notes", map[string]any{"seed": x})
	call("seed_prime", map[string]any{})
	call("seed_guide", map[string]any{})
	call("seed_tend", map[string]any{"seed": x})
	call("seed_pulse", map[string]any{"seed": x, "text": "Running retry tests"})
	call("seed_bed_add", map[string]any{"name": "Checkout", "note": "Payments flow", "repos": []any{"shop"}})
	call("seed_bed_ls", map[string]any{})
	call("seed_bed_set", map[string]any{"bed": "Checkout", "note": "Updated note"})
	call("seed_bed_assign", map[string]any{"seed": x, "bed": "Checkout"})
	call("seed_bed_rm", map[string]any{"bed": "Checkout"})
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

	shapes.finish("seed_")
}

func TestEveryCommandRefusesAnArgumentItDoesNotTakeSayingWhichItDoes(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	offered := listOfferedTools(t, agent)
	text, failed := agent.script(t, "attn_do", `const refused = {};
for (const name of Object.keys(globalThis)) {
	if (typeof globalThis[name] !== "function" || name === "Refusal") continue;
	try { globalThis[name]({session: "someone-else"}) } catch (e) { refused[name] = {code: e.code, message: e.message, fix: e.fix} }
}
return refused`)
	var refused map[string]struct{ Code, Message, Fix string }
	if err := json.Unmarshal([]byte(text), &refused); failed || err != nil {
		t.Fatalf("a script calling every command with an argument none takes answered %q: %v", text, err)
	}
	for tool := range offered {
		if got := refused[tool]; got.Code != "usage" || !strings.Contains(got.Message, `"session"`) || !strings.HasPrefix(got.Fix, "Call "+tool+" with only the arguments it takes") || !strings.Contains(got.Fix, "\n"+tool+"(") {
			t.Errorf("%s with an argument it does not take was refused with %+v, want a usage refusal naming the argument, with a fix", tool, got)
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
	shown, err := w.Client().SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range shown.Seed.Checks {
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

func TestARefusalNamesTheToolToAToolCallerAndTheCommandToTheCLI(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	seed := plantForTools(t, w)

	refused := agent.mustCall(t, "seed_standby", map[string]any{"seed": seed, "next": "Resume the rollout.", "stay": true})
	want := "not_tender: seed standby: only " + seed + "'s tender can put it on standby, from its own session; tend it first: `seed_tend({seed: \"" + seed + "\"})`"
	if text := resultText(refused); !refused.IsError || text != want {
		t.Errorf("seed_standby on a seed nobody tends answered\n%q\nwant\n%q", text, want)
	}

	err := w.Client().RunCommand("seed standby", protocol.SessionID(session), map[string]any{"seed": seed, "next": "Resume the rollout.", "stay": true}, &struct{}{})
	cli := "only " + seed + "'s tender can put it on standby, from its own session; tend it first: attn seed tend " + seed
	if err == nil || !strings.HasSuffix(err.Error(), cli) {
		t.Errorf("seed standby through run_command answered %v, want it to end with %q", err, cli)
	}
}
