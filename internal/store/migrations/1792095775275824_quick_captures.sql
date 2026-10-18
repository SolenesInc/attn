CREATE TABLE quick_captures (
 profile_id TEXT NOT NULL, id TEXT NOT NULL, inbox_item_id TEXT NOT NULL UNIQUE, submission TEXT NOT NULL, mailbox_kind TEXT NOT NULL,
 mailbox_member_id TEXT NOT NULL DEFAULT '', body TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(profile_id,id)
);
CREATE INDEX quick_captures_recent ON quick_captures(profile_id,created_at DESC,id DESC);
CREATE TABLE quick_capture_attachments (
 profile_id TEXT NOT NULL, capture_id TEXT NOT NULL, attachment_id TEXT NOT NULL, name TEXT NOT NULL,
 media_type TEXT NOT NULL DEFAULT '', byte_count INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL, position INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(profile_id, capture_id, attachment_id)
);
