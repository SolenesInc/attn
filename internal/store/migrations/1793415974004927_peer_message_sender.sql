ALTER TABLE peer_messages RENAME COLUMN sender_session_id TO sender;
UPDATE peer_messages SET sender = COALESCE(
  (SELECT 'member:' || s.member_key FROM sessions s WHERE s.id = peer_messages.sender AND s.member_key != ''),
  'session:' || sender);
