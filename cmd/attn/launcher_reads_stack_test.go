package main_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// The pair shares one disk, so a read answers the same files on either
// machine. What tells the machines apart is their own state: each registry
// knows only the worktrees created on it, and each has its own harnesses.
func TestLauncherReadsRunOnTheNamedOutpost(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	home.Vars = append(home.Vars, "ATTN_CODEX_EXECUTABLE="+home.Path("not-installed", "codex"))
	outpost.Vars = append(outpost.Vars, "ATTN_CLAUDE_EXECUTABLE="+outpost.Path("not-installed", "claude"))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	app, host := pair.App, pair.Host
	onOutpost := protocol.Ptr(host)

	repo := outpost.Path("shop")
	gitRepo(t, repo)
	worktree := outpost.Path("shop--feat")
	created := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, Host: onOutpost, MainRepo: repo, Branch: "feat", Path: protocol.Ptr(worktree), RequestID: protocol.Ptr("create")},
		protocol.EventCreateWorktreeResult, func(r protocol.CreateWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == "create" })
	if !created.Success {
		t.Fatalf("create worktree on the outpost: %s", protocol.Deref(created.Error))
	}

	registered := func(p *testworld.Peer, id string) bool {
		listed := testworld.Request(p, protocol.WorktreeListMessage{Cmd: protocol.CmdWorktreeList, RequestID: protocol.Ptr(id)},
			protocol.EventWorktreeListResult, func(r protocol.WorktreeListResultEvent) bool { return protocol.Deref(r.RequestID) == id })
		for _, wt := range listed.WorktreeListResult.Worktrees {
			if wt.Path == worktree {
				return true
			}
		}
		return false
	}
	outpostApp := outpost.App()
	if !registered(outpostApp, "outpost-before") || registered(app, "home-before") {
		t.Fatalf("the outpost-created worktree must be in the outpost's registry only")
	}

	repoInfo := func(id string, on *string) protocol.GetRepoInfoResultMessage {
		return testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Host: on, Repo: repo, RequestID: protocol.Ptr(id)},
			protocol.EventGetRepoInfoResult, func(r protocol.GetRepoInfoResultMessage) bool { return protocol.Deref(r.RequestID) == id })
	}
	createdAt := func(r protocol.GetRepoInfoResultMessage) (string, bool) {
		if !r.Success || r.Info == nil {
			t.Fatalf("get_repo_info: %s", protocol.Deref(r.Error))
		}
		for _, wt := range r.Info.Worktrees {
			if wt.Path == worktree {
				return protocol.Deref(wt.CreatedAt), true
			}
		}
		return "", false
	}
	if stamp, listed := createdAt(repoInfo("info-outpost", onOutpost)); !listed || stamp == "" {
		t.Errorf("get_repo_info on the outpost lists the worktree=%v created_at=%q; want the outpost registry's created_at", listed, stamp)
	}
	if stamp, listed := createdAt(repoInfo("info-home", nil)); !listed || stamp != "" {
		t.Errorf("get_repo_info on the home lists the worktree=%v created_at=%q; want it listed from git without a created_at", listed, stamp)
	}

	browsed := testworld.Request(app, protocol.BrowseDirectoryMessage{Cmd: protocol.CmdBrowseDirectory, Host: onOutpost, InputPath: outpost.Path() + "/sh", RequestID: protocol.Ptr("browse")},
		protocol.EventBrowseDirectoryResult, func(r protocol.BrowseDirectoryResultMessage) bool { return protocol.Deref(r.RequestID) == "browse" })
	machineHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !browsed.Success || protocol.Deref(browsed.HomePath) != machineHome || len(browsed.Entries) != 2 || !protocol.Deref(browsed.Entries[0].IsRepo) {
		t.Errorf("browse on the outpost = %+v; want shop and shop--feat with the outpost's home_path %s", browsed, machineHome)
	}

	inspected := testworld.Request(app, protocol.InspectPathMessage{Cmd: protocol.CmdInspectPath, Host: onOutpost, Path: worktree, RequestID: protocol.Ptr("inspect")},
		protocol.EventInspectPathResult, func(r protocol.InspectPathResultMessage) bool { return protocol.Deref(r.RequestID) == "inspect" })
	if !inspected.Success || inspected.Inspection == nil || !inspected.Inspection.IsDirectory || protocol.Deref(inspected.Inspection.RepoRoot) == "" {
		t.Errorf("inspect on the outpost = %+v; want the worktree as a repository", inspected)
	}

	available := func(id, on string) map[string]bool {
		r := testworld.Request(app, protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: on, RequestID: protocol.Ptr(id)},
			protocol.EventAgentCatalogResult, func(r protocol.AgentCatalogResultMessage) bool { return protocol.Deref(r.RequestID) == id })
		if !r.Success || r.Catalog == nil || r.Host != on {
			t.Fatalf("agent_catalog %q = %+v", on, r)
		}
		harnesses := map[string]bool{}
		for _, h := range r.Catalog.Harnesses {
			harnesses[h.ID] = h.Available
		}
		return harnesses
	}
	if onOutpostHarnesses := available("catalog-outpost", host); !onOutpostHarnesses["codex"] || onOutpostHarnesses["claude"] {
		t.Errorf("agent_catalog on the outpost = %v; want codex and not claude", onOutpostHarnesses)
	}
	if onHome := available("catalog-home", ""); !onHome["claude"] || onHome["codex"] {
		t.Errorf("agent_catalog on the home = %v; want claude and not codex", onHome)
	}

	deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Host: onOutpost, Path: worktree, RequestID: protocol.Ptr("delete")},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return protocol.Deref(r.RequestID) == "delete" })
	if !deleted.Success {
		t.Fatalf("delete worktree on the outpost: %s", protocol.Deref(deleted.Error))
	}
	if registered(outpostApp, "outpost-after") {
		t.Errorf("the deleted worktree is still in the outpost's registry")
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("the deleted worktree is still on disk: %v", err)
	}

	lost := app.EventCount()
	pair.Disconnect()
	outpostStateSince(app, "outpost", protocol.OutpostStateLost, lost)
	unreachable := "outpost is unreachable"
	failed := map[string]string{}
	failed["browse_directory"] = protocol.Deref(testworld.Request(app, protocol.BrowseDirectoryMessage{Cmd: protocol.CmdBrowseDirectory, Host: onOutpost, InputPath: "~/", RequestID: protocol.Ptr("browse-lost")},
		protocol.EventBrowseDirectoryResult, func(r protocol.BrowseDirectoryResultMessage) bool {
			return protocol.Deref(r.RequestID) == "browse-lost" && !r.Success
		}).Error)
	failed["inspect_path"] = protocol.Deref(testworld.Request(app, protocol.InspectPathMessage{Cmd: protocol.CmdInspectPath, Host: onOutpost, Path: repo, RequestID: protocol.Ptr("inspect-lost")},
		protocol.EventInspectPathResult, func(r protocol.InspectPathResultMessage) bool {
			return protocol.Deref(r.RequestID) == "inspect-lost" && !r.Success
		}).Error)
	failed["get_repo_info"] = protocol.Deref(testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Host: onOutpost, Repo: repo, RequestID: protocol.Ptr("info-lost")},
		protocol.EventGetRepoInfoResult, func(r protocol.GetRepoInfoResultMessage) bool {
			return protocol.Deref(r.RequestID) == "info-lost" && !r.Success
		}).Error)
	failed["delete_worktree"] = protocol.Deref(testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Host: onOutpost, Path: repo, RequestID: protocol.Ptr("delete-lost")},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool {
			return protocol.Deref(r.RequestID) == "delete-lost" && !r.Success
		}).Error)
	failed["agent_catalog"] = protocol.Deref(testworld.Request(app, protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: host, RequestID: protocol.Ptr("catalog-lost")},
		protocol.EventAgentCatalogResult, func(r protocol.AgentCatalogResultMessage) bool {
			return protocol.Deref(r.RequestID) == "catalog-lost" && !r.Success
		}).Error)
	for cmd, reason := range failed {
		if !strings.Contains(reason, unreachable) {
			t.Errorf("%s with the outpost lost failed with %q; want it to say %q", cmd, reason, unreachable)
		}
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("the repository must survive a delete sent to a lost outpost: %v", err)
	}
	t.Logf("outpost reads answered; with the outpost lost: %v", failed)
}
