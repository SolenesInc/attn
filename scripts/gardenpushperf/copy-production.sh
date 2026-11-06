#!/usr/bin/env bash
# Copies a database read-only into a named instance, production's by default,
# and turns off what would reach real repositories, worktrees, conversations,
# the Notebook or other machines. Every copied session moves to an outpost that
# does not exist, so the copy shows it offline and never runs it.
set -euo pipefail

name=${1:?usage: copy-production.sh <instance> [<source db>]}
source_db=${2:-$HOME/.attn/attn.db}
attn=${ATTN:-./attn}
data=$(ATTN_INSTANCE=$name "$attn" instance resolve --field dataDir)
if [ "$data" = "$HOME/.attn" ]; then
  echo "copy-production.sh: $name resolves to the production data directory" >&2
  exit 1
fi
if [ -e "$data/attn.db" ] || [ -e "$data/attn.sock" ]; then
  echo "copy-production.sh: $name already has data in $data; run attn instance clean $name first" >&2
  exit 1
fi

mkdir -p "$data/notebook-scratch"
sqlite3 "file:$source_db?mode=ro" ".backup '$data/attn.db.copying'"

drops=$(sqlite3 "$data/attn.db.copying" "SELECT 'DROP TRIGGER ' || name || ';' FROM sqlite_master WHERE type = 'trigger' AND tbl_name IN ('settings', 'sessions')")
creates=$(sqlite3 "$data/attn.db.copying" "SELECT sql || ';' FROM sqlite_master WHERE type = 'trigger' AND tbl_name IN ('settings', 'sessions')")
crew_in_data_dir=$(sqlite3 "$data/attn.db.copying" "SELECT 'UPDATE doc_' || id || ' SET body = json_set(json_remove(body, ''\$.home_dir'', ''\$.cwd''), ''\$.awareness_dirs'', json(''[]''));' FROM document_collections WHERE namespace = 'core/crew' AND collection = 'members'")
sqlite3 "$data/attn.db.copying" <<SQL
BEGIN;
$drops
DELETE FROM settings WHERE key IN ('notebook.root', 'worktree_sweep_enabled', 'outposts_enabled', 'analytics.sharing_enabled');
INSERT INTO settings (key, value) VALUES
  ('notebook.root', '$data/notebook-scratch'),
  ('worktree_sweep_enabled', 'false'),
  ('outposts_enabled', 'false'),
  ('analytics.sharing_enabled', 'false');
INSERT OR IGNORE INTO outposts (id, name, destination, added_at) VALUES
  ('copy-source', 'copy source', 'copy-source.invalid', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
UPDATE sessions SET host = 'copy-source', copied_from = '', link = '';
DELETE FROM delegation_operations WHERE state IN ('accepted', 'preparing');
UPDATE automation_runs SET state = 'cancelled', cancel_reason = 'copied' WHERE state = 'pending';
$creates
UPDATE profile_settings SET value = '$data/notebook-scratch' WHERE key = 'notebook.root';
$crew_in_data_dir
UPDATE automation_definitions SET enabled = 0;
COMMIT;
SQL
sqlite3 "$data/attn.db.copying" ".backup '$data/attn.db'"
rm -f "$data/attn.db.copying" "$data/attn.db.copying-wal" "$data/attn.db.copying-shm"

sqlite3 "$data/attn.db" "SELECT key || '=' || value FROM settings WHERE key IN ('notebook.root', 'worktree_sweep_enabled', 'outposts_enabled', 'analytics.sharing_enabled') ORDER BY key; SELECT 'profile notebook.root=' || value FROM profile_settings WHERE key = 'notebook.root'; SELECT count(*) || ' automations enabled' FROM automation_definitions WHERE enabled != 0; SELECT count(*) || ' sessions on this machine' FROM sessions WHERE host != 'copy-source';"
