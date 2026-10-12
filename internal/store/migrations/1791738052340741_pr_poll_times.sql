CREATE TABLE pr_poll_times (
    pr_id TEXT PRIMARY KEY REFERENCES prs(id) ON DELETE CASCADE,
    last_updated TEXT NOT NULL,
    last_polled TEXT NOT NULL
);

INSERT INTO pr_poll_times (pr_id, last_updated, last_polled)
SELECT id, last_updated, last_polled FROM prs;

ALTER TABLE prs DROP COLUMN last_updated;
ALTER TABLE prs DROP COLUMN last_polled;
ALTER TABLE pull_request_watches DROP COLUMN last_success_at;
