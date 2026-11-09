CREATE TABLE update_run_outposts (
    run_id TEXT NOT NULL,
    outpost_id TEXT NOT NULL,
    name TEXT NOT NULL,
    state TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, outpost_id)
);
