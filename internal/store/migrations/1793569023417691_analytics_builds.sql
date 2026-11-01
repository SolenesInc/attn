CREATE TABLE analytics_builds (
    id    INTEGER PRIMARY KEY,
    build TEXT NOT NULL UNIQUE
);

ALTER TABLE analytics_outbox ADD COLUMN build_id INTEGER REFERENCES analytics_builds(id);
