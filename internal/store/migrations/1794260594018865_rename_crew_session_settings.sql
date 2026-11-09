UPDATE settings SET key = 'crew.away_close_enabled' WHERE key = 'crew.autosleep_enabled';
UPDATE settings SET key = 'crew.mail_start_limit' WHERE key = 'crew.wake_limit';
UPDATE settings SET key = 'crew.mail_start_window_seconds' WHERE key = 'crew.wake_limit_window_seconds';
UPDATE settings SET key = 'garden.tender_resume' WHERE key = 'garden.tender_wake';
UPDATE profile_settings SET key = 'garden.tender_resume' WHERE key = 'garden.tender_wake';
