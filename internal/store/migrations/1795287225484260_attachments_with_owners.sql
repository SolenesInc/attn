CREATE TABLE attachments (
 attachment_id TEXT NOT NULL PRIMARY KEY, profile_id TEXT NOT NULL, owner_kind TEXT NOT NULL, owner_id TEXT NOT NULL,
 name TEXT NOT NULL, media_type TEXT NOT NULL DEFAULT '', byte_count INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL, position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX attachments_by_owner ON attachments(profile_id, owner_kind, owner_id);
INSERT INTO attachments(attachment_id, profile_id, owner_kind, owner_id, name, media_type, byte_count, state, position)
 SELECT attachment_id, profile_id, 'quick_capture', capture_id, name, media_type, byte_count, state, position FROM quick_capture_attachments;
DROP TABLE quick_capture_attachments;
