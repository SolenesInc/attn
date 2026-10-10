ALTER TABLE peer_messages RENAME COLUMN sender_session_id TO sender;
ALTER TABLE pull_request_watches RENAME COLUMN address TO watcher;
ALTER TABLE presentations RENAME COLUMN address TO handback_to;
