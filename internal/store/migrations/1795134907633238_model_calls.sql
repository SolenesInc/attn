CREATE TABLE model_calls (
  id INTEGER PRIMARY KEY,
  profile_id TEXT NOT NULL,
  at TEXT NOT NULL,
  sender TEXT NOT NULL COLLATE NOCASE,
  purpose TEXT NOT NULL,
  harness TEXT NOT NULL,
  model TEXT NOT NULL,
  took_ms INTEGER NOT NULL,
  model_ms INTEGER NOT NULL,
  tokens_in INTEGER NOT NULL,
  tokens_out INTEGER NOT NULL,
  cost_usd REAL NOT NULL,
  outcome TEXT NOT NULL
);
CREATE INDEX model_calls_profile_at ON model_calls(profile_id, at);
