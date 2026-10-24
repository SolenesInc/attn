CREATE TABLE analytics_outbox (
    id      INTEGER PRIMARY KEY,
    stream  TEXT NOT NULL,
    seq     INTEGER NOT NULL,
    at      TEXT NOT NULL,
    record  TEXT NOT NULL,
    sent_at TEXT,
    UNIQUE (stream, seq)
);
CREATE INDEX idx_analytics_outbox_at ON analytics_outbox(at);
CREATE INDEX idx_analytics_outbox_stream ON analytics_outbox(stream, id);
