package daemon

import "testing"

func TestClaudeHasTrustFollowsTheMeasuredRule(t *testing.T) {
	inWorktree := claudeCheckout{top: "/code/shop--wt", repository: "/code/shop"}
	inMain := claudeCheckout{top: "/code/shop", repository: "/code/shop"}
	plain := claudeCheckout{}
	for _, c := range []struct {
		name     string
		trusted  string
		cwd      string
		checkout claudeCheckout
		want     bool
	}{
		{"main repository covers its worktree", "/code/shop", "/code/shop--wt", inWorktree, true},
		{"main repository covers a worktree's subfolder", "/code/shop", "/code/shop--wt/pkg", inWorktree, true},
		{"main repository covers its own subfolder", "/code/shop", "/code/shop/pkg", inMain, true},
		{"worktree covers itself", "/code/shop--wt", "/code/shop--wt", inWorktree, true},
		{"worktree covers its subfolder", "/code/shop--wt", "/code/shop--wt/pkg", inWorktree, true},
		{"worktree does not cover the main repository", "/code/shop--wt", "/code/shop", inMain, false},
		{"parent folder does not cover a repository below it", "/code", "/code/shop", inMain, false},
		{"parent folder does not cover a worktree below it", "/code", "/code/shop--wt", inWorktree, false},
		{"parent folder covers a plain folder below it", "/code", "/code/notes/today", plain, true},
		{"nothing trusts a plain folder", "", "/code/notes", plain, false},
		{"nothing trusts a repository", "", "/code/shop", inMain, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			projects := map[string]claudeProject{}
			if c.trusted != "" {
				projects[c.trusted] = claudeProject{HasTrustDialogAccepted: true}
			}
			if got := claudeHasTrust(projects, c.cwd, c.checkout); got != c.want {
				t.Fatalf("claudeHasTrust = %v, want %v", got, c.want)
			}
		})
	}
}
