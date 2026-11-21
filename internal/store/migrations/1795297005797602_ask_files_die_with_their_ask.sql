CREATE TRIGGER ask_files_die_with_their_ask AFTER DELETE ON asks
BEGIN
  UPDATE attachments SET state = 'discarded'
  WHERE owner_kind = 'ask' AND owner_id = OLD.id AND state IN ('staged', 'ready');
END;
