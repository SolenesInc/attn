package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAFailedGitOperationsRecordNamesWhyItFailed(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	for _, row := range []struct {
		name     string
		kind     protocol.GitOperationKind
		failure  protocol.GitFailure
		exitCode *int
		arrange  func(t *testing.T, shop, path string)
	}{
		{"deleting a worktree whose repository folder is gone", protocol.GitOperationKindDeleteWorktree, protocol.GitFailureMissingDirectory, nil, func(t *testing.T, shop, _ string) {
			if err := os.RemoveAll(shop); err != nil {
				t.Fatal(err)
			}
		}},
		{"a folder that is no longer a repository", protocol.GitOperationKindRefreshRepository, protocol.GitFailureExited, protocol.Ptr(128), func(t *testing.T, shop, _ string) {
			if err := os.Rename(filepath.Join(shop, ".git"), filepath.Join(shop, ".git-gone")); err != nil {
				t.Fatal(err)
			}
		}},
		{"an integration branch that no longer resolves", protocol.GitOperationKindRefreshRepository, protocol.GitFailureIntegrationRefUnresolved, nil, func(t *testing.T, shop, _ string) {
			runGit(t, shop, "branch", "-m", "main", "trunk")
		}},
		{"stashes git cannot read", protocol.GitOperationKindRefreshRepository, protocol.GitFailureStashUnreadable, nil, func(t *testing.T, shop, path string) {
			if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, path, "stash", "push", "-m", "keep me")
			if err := os.WriteFile(filepath.Join(shop, ".git", "refs", "stash"), []byte(strings.Repeat("0", 39)+"1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"deleting a locked worktree", protocol.GitOperationKindDeleteWorktree, protocol.GitFailureExited, protocol.Ptr(128), func(t *testing.T, _, path string) {
			runGit(t, path, "worktree", "lock", path)
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				setAnalyticsEnabled(app, "true")
				shop := newRepo(t, "shop")
				path := createWorktree(t, app, shop, "feat-work")
				refreshWorktrees(t, cli)
				synctest.Wait()
				row.arrange(t, shop, path)
				if row.kind == protocol.GitOperationKindDeleteWorktree {
					app.Send(protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: path})
				} else {
					refreshWorktrees(t, cli)
				}
				synctest.Wait()

				var failed, succeeded int
				for _, record := range mirroredRecords(t, app, "git.operation.finished") {
					var op struct {
						Kind     protocol.GitOperationKind   `json:"kind"`
						Status   protocol.GitOperationStatus `json:"status"`
						Failure  *protocol.GitFailure        `json:"failure"`
						ExitCode *int                        `json:"exit_code"`
						Path     json.RawMessage             `json:"path"`
						Error    json.RawMessage             `json:"error"`
					}
					if err := json.Unmarshal(record.Props, &op); err != nil {
						t.Fatalf("props %s: %v", record.Props, err)
					}
					switch op.Status {
					case protocol.GitOperationStatusSucceeded:
						succeeded++
						if op.Failure != nil || op.ExitCode != nil || op.Error != nil {
							t.Errorf("a succeeded operation carries a failure: %s", record.Props)
						}
					case protocol.GitOperationStatusFailed:
						failed++
						if op.Kind != row.kind || op.Failure == nil || *op.Failure != row.failure || !equalPtr(op.ExitCode, row.exitCode) {
							t.Errorf("the failed operation carries %s, want a %s with failure %s and exit_code %v", record.Props, row.kind, row.failure, protocol.Deref(row.exitCode))
						}
						if !isLength(op.Path) || !isLength(op.Error) {
							t.Errorf("the failed operation ships its path or error as text: %s", record.Props)
						}
					}
				}
				if failed == 0 || succeeded == 0 {
					t.Errorf("the outbox holds %d failed and %d succeeded git operations, want both", failed, succeeded)
				}
			})
		})
	}
}

func equalPtr[T comparable](a, b *T) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func isLength(raw json.RawMessage) bool {
	var length struct {
		S *int `json:"s"`
	}
	return json.Unmarshal(raw, &length) == nil && length.S != nil
}
