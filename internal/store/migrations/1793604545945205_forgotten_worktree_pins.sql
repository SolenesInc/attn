CREATE TABLE forgotten_worktree_pins (
    path TEXT PRIMARY KEY NOT NULL,
    main_repo TEXT NOT NULL,
    pinned_at TEXT NOT NULL
);
