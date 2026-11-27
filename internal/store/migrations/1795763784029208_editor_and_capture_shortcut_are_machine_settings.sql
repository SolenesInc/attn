DELETE FROM settings WHERE key IN ('editor_executable', 'capture.shortcut') AND copied_from != '';
DELETE FROM outpost_outbox WHERE tbl = 'settings' AND key IN ('editor_executable', 'capture.shortcut');
