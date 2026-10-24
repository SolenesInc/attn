CREATE TABLE vents (
    id           TEXT PRIMARY KEY,
    profile_id   TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    turn         TEXT NOT NULL,
    text         TEXT NOT NULL,
    context      TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    withdrawn_at TEXT,
    dismissed_at TEXT
);
CREATE UNIQUE INDEX idx_vents_one_per_turn ON vents(session_id, turn) WHERE withdrawn_at IS NULL;
CREATE INDEX idx_vents_profile ON vents(profile_id, created_at);
