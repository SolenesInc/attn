CREATE TABLE monitored_worktree_repositories (
    main_repo TEXT PRIMARY KEY NOT NULL
);

INSERT INTO monitored_worktree_repositories (main_repo)
SELECT main_repo FROM worktrees WHERE main_repo != ''
UNION
SELECT main_repo FROM sessions WHERE main_repo != '' AND closed_at = ''
UNION
SELECT main_repo FROM repo_integration_branches WHERE main_repo != ''
UNION
SELECT main_repo FROM worktree_sweep_log WHERE main_repo != '';
