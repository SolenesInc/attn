CREATE TABLE session_subagent_feeds (
    session_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    server_entry INTEGER NOT NULL DEFAULT 0,
    server_usage_baseline TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, conversation_id)
);
