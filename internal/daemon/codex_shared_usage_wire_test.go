package daemon_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSharedCodexCloseIncludesItsFirstAvailableUsageWithoutAnEarlierRead(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	id := w.Spawn(app, fakeagent.Codex, w.Path("first"))
	run := w.Launched(id)
	pane := awaitSharedView(app, id, id)
	run.UsageOnArchive("first final usage")
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: queriedSession(t, w.Client(), id).WorkspaceID, PaneID: pane.PaneID}, protocol.CmdWorkspaceLayoutClosePane, queriedSession(t, w.Client(), id).WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	closed := awaitClosed(app, id)
	if closed.Usage == nil || closed.Usage.TotalTokens != claudeTokens("first final usage") || protocol.Deref(closed.Usage.MeasurementIncomplete) {
		t.Fatalf("first usage at close = %+v", closed.Usage)
	}
	assertLedgerUsage(t, w.Client(), id, closed.Usage)
}

func TestSharedCodexNativeCrashLeavesUsageExplicitlyIncomplete(t *testing.T) {
	for _, hasUsage := range []bool{false, true} {
		t.Run(fmt.Sprint(hasUsage), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			id := w.Spawn(app, fakeagent.Codex, w.Path("crash"))
			run := w.Launched(id)
			awaitSharedView(app, id, id)
			counted := 0
			if hasUsage {
				app.TypeLine(id, "work")
				run.Prompted()
				run.Reply("saved <!-- attn:state=idle -->")
				counted = claudeTokens("saved <!-- attn:state=idle -->")
				awaitUsageTokens(app, id, counted)
			}
			run.Exit(23)
			usage := testworld.AwaitSession(app, id, func(s protocol.Session) bool {
				return s.Usage != nil && protocol.Deref(s.Usage.MeasurementIncomplete)
			}).Usage
			if usage.TotalTokens != counted {
				t.Fatalf("crash usage = %+v, want %d available tokens", usage, counted)
			}
			assertLedgerUsage(t, w.Client(), id, usage)
		})
	}
}

func TestSharedCodexUsageKeepsModelAndFastPricingChanges(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	id := w.Spawn(app, fakeagent.Codex, w.Path("pricing"))
	run := w.Launched(id)
	awaitSharedView(app, id, id)
	counted, cost := 0, 0.0
	for _, tier := range []string{"priority", "default", "fast"} {
		run.CodexSettings("gpt-6.1-sol", tier)
		app.TypeLine(id, "continue")
		run.Prompted()
		run.Reply("priced <!-- attn:state=idle -->")
		counted += claudeTokens("priced <!-- attn:state=idle -->")
		multiplier := 1.0
		if tier != "default" {
			multiplier = 2
		}
		cost += float64(len("priced <!-- attn:state=idle -->")) * 12 * multiplier / 1e6
		usage := awaitUsageTokens(app, id, counted)
		if usage.CostUsd == nil || math.Abs(*usage.CostUsd-cost) > 1e-12 || usage.HasUnpricedUsage {
			t.Fatalf("tier %s usage = %+v, want %.9f", tier, usage, cost)
		}
		assertLedgerUsage(t, w.Client(), id, usage)
	}
	run.CodexSettings("unpriced-fixture-model", "default")
	app.TypeLine(id, "unknown model")
	run.Prompted()
	run.Reply("unpriced <!-- attn:state=idle -->")
	usage := awaitUsageTokens(app, id, counted+claudeTokens("unpriced <!-- attn:state=idle -->"))
	if !usage.HasUnpricedUsage || len(usage.Models) != 2 {
		t.Fatalf("unknown model visibility = %+v", usage)
	}
	assertLedgerUsage(t, w.Client(), id, usage)
}

func TestSharedCodexUsageBelongsToTheOwnerAcrossViewsCloseAndReopen(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentA := w.Launched(a)
	first := awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)

	app.TypeLine(a, "first A turn")
	agentA.Prompted()
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	app.TypeLine(b, "first B turn")
	agentB.Prompted()
	agentB.Reply("B reply <!-- attn:state=idle -->")
	agentA.Subagent("A child")
	agentA.Subagent("A grandchild")
	agentA.GuardianReview("A native review")
	agentA.Reply("A hidden reply <!-- attn:state=idle -->")
	aTokens := claudeTokens("A child") + claudeTokens("A grandchild") + claudeTokens("A native review") + claudeTokens("A hidden reply <!-- attn:state=idle -->")
	bTokens := claudeTokens("B reply <!-- attn:state=idle -->")
	usageA := awaitUsageTokens(app, a, aTokens)
	awaitUsageTokens(app, b, bTokens)
	assertLedgerUsage(t, cli, a, usageA)

	attached, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	second := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.SessionID) == a && protocol.Deref(p.RuntimeID) != a && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	app.TypeLine(a, "/agents "+agentA.ConversationID)
	first = awaitSharedView(app, a, a)
	agentA.BroadcastUsage()
	agentA.BroadcastUsage()
	assertLedgerUsage(t, cli, a, usageA)

	app.TypeLine(a, "work during close")
	agentA.Prompted()
	final := "final usage available at archive"
	agentA.UsageOnArchive(final)
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: second.WorkspaceLayout.WorkspaceID, PaneID: protocol.Deref(attached.PaneID)}, protocol.CmdWorkspaceLayoutClosePane, second.WorkspaceLayout.WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	if shown, err := cli.SessionShow(a); err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("extra-view close ended A: %+v (%v)", shown, err)
	}
	assertLedgerUsage(t, cli, a, usageA)
	result = workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: second.WorkspaceLayout.WorkspaceID, PaneID: first.PaneID}, protocol.CmdWorkspaceLayoutClosePane, second.WorkspaceLayout.WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	closed := awaitClosed(app, a)
	aTokens += claudeTokens(final)
	if closed.Usage == nil || closed.Usage.TotalTokens != aTokens || protocol.Deref(closed.Usage.MeasurementIncomplete) {
		t.Fatalf("closed usage = %+v, want %d available tokens", closed.Usage, aTokens)
	}
	assertLedgerUsage(t, cli, a, closed.Usage)
	if usage := queriedSession(t, cli, b).Usage; usage == nil || usage.TotalTokens != bTokens {
		t.Fatalf("closing A changed B usage: %+v", usage)
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a}); err != nil {
		t.Fatal(err)
	}
	assertLedgerUsage(t, cli, a, closed.Usage)
	resultInput := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "cost-reopened", SessionID: a, Text: "continue A"}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == "cost-reopened" })
	if resultInput.Status != "delivered" {
		t.Fatal(resultInput)
	}
	agentA.Prompted()
	agentA.Reply("A resumed reply <!-- attn:state=idle -->")
	usageA = awaitUsageTokens(app, a, aTokens+claudeTokens("A resumed reply <!-- attn:state=idle -->"))
	assertLedgerUsage(t, cli, a, usageA)
}

func assertLedgerUsage(t *testing.T, cli *client.Client, id string, want *protocol.SessionUsage) {
	t.Helper()
	shown, err := cli.SessionShow(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shown.Entry.Usage, want) {
		t.Fatalf("show usage = %+v (%v), want %+v", shown, err, want)
	}
	page, err := cli.SessionList(client.SessionListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if entry.ID == id {
			if !reflect.DeepEqual(entry.Usage, want) {
				t.Fatalf("list usage = %+v, want %+v", entry.Usage, want)
			}
			return
		}
	}
	t.Fatalf("ledger omitted %s", id)
}

func TestSharedCodexHiddenOwnerUsageContinuesAcrossDaemonRestart(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	a := stack.Spawn(app, fakeagent.Codex, stack.Path("same"))
	agentA := stack.Launched(a)
	awaitSharedView(app, a, a)
	app.TypeLine(a, "first turn")
	agentA.Prompted()
	agentA.Reply("first <!-- attn:state=idle -->")
	counted := claudeTokens("first <!-- attn:state=idle -->")
	awaitUsageTokens(app, a, counted)
	b := stack.Spawn(app, fakeagent.Codex, stack.Path("same"))
	agentB := stack.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	stack.Stop()
	stack.Start()
	app = stack.App()
	result := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "cost-hidden-restart", SessionID: a, Text: "hidden work"}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool {
		return e.RequestID == "cost-hidden-restart"
	})
	if result.Status != "delivered" {
		t.Fatal(result)
	}
	agentA.Prompted()
	agentA.Reply("hidden after restart <!-- attn:state=idle -->")
	counted += claudeTokens("hidden after restart <!-- attn:state=idle -->")
	usage := awaitUsageTokens(app, a, counted)
	assertLedgerUsage(t, stack.Client(), a, usage)
	output := stack.Attn("session", "show", a)
	if !strings.Contains(output.Stdout, fmt.Sprintf("usage      %d tokens", counted)) {
		t.Fatalf("CLI omitted saved usage: %s", output.Stdout)
	}
}

// A real PTY backend with a failed cleanup boundary; all other operations remain native.
type failingCostViewRemoval struct {
	*ptybackend.EmbeddedBackend
	id   string
	fail atomic.Bool
}

func (b *failingCostViewRemoval) Remove(ctx context.Context, id string) error {
	if id == b.id && b.fail.Load() {
		return fmt.Errorf("view cleanup unavailable")
	}
	return b.EmbeddedBackend.Remove(ctx, id)
}

func TestSharedCodexFailedPostArchiveCleanupKeepsOwnerAccountingLive(t *testing.T) {
	backend := &failingCostViewRemoval{EmbeddedBackend: ptybackend.NewEmbedded(nil), id: "accounting-cleanup"}
	w := &world{World: prepareWorld(t, fakeagent.Codex), backend: backend}
	w.start()
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	repo := reopenRepoWithOrigin(t)
	cwd := reopenWorktree(t, repo, "feat/accounting-cleanup")
	id := w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = backend.id })
	run := w.Launched(id)
	awaitSharedView(app, id, id)
	run.UsageOnArchive("available at archive")
	backend.fail.Store(true)
	result := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: cwd, Force: protocol.Ptr(true)}, protocol.EventDeleteWorktreeResult, func(e protocol.DeleteWorktreeResultMessage) bool { return e.Path == cwd })
	if result.Success {
		t.Fatal("failed view cleanup unexpectedly finalized the worktree close")
	}
	shown, err := cli.SessionShow(id)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("cleanup failure closed the owner: %+v %v", shown, err)
	}
	tokens := claudeTokens("available at archive")
	usage := awaitUsageTokens(app, id, tokens)
	assertLedgerUsage(t, cli, id, usage)
	backend.fail.Store(false)
	closeSession(t, w.Client(), id, "cleanup recovered")
	closed := awaitClosed(app, id)
	if closed.Usage == nil || closed.Usage.TotalTokens != tokens || protocol.Deref(closed.Usage.MeasurementIncomplete) {
		t.Fatalf("retry changed available totals: %+v", closed.Usage)
	}
	assertLedgerUsage(t, w.Client(), id, closed.Usage)
}

func TestSharedCodexArchivedOpenOwnerUsageContinuesAcrossDaemonRestart(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.StartCrashingAt("codex-owner-native-archived")
	app := stack.App()
	sharedCodexSetting(t, app, true)
	id := stack.Spawn(app, fakeagent.Codex, stack.Path("archived-open"))
	run := stack.Launched(id)
	awaitSharedView(app, id, id)
	run.UsageOnArchive("available before interrupted close")
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: id})
	stack.AwaitCrash()
	stack.Start()
	app = stack.App()
	shown, err := stack.Client().SessionShow(id)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("interrupted close finalized its owner: %+v %v", shown, err)
	}
	tokens := claudeTokens("available before interrupted close")
	usage := awaitUsageTokens(app, id, tokens)
	assertLedgerUsage(t, stack.Client(), id, usage)
	closeSession(t, stack.Client(), id, "retry interrupted close")
	closed := awaitClosed(app, id)
	if closed.Usage == nil || closed.Usage.TotalTokens != tokens || protocol.Deref(closed.Usage.MeasurementIncomplete) {
		t.Fatalf("retry changed available totals: %+v", closed.Usage)
	}
	assertLedgerUsage(t, stack.Client(), id, closed.Usage)
}
