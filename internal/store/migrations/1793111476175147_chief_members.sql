ALTER TABLE profiles ADD COLUMN chief_member TEXT NOT NULL DEFAULT '';
CREATE TABLE chief_upgrades (
 profile_id TEXT PRIMARY KEY,
 chief_member TEXT NOT NULL UNIQUE,
 previous_session TEXT NOT NULL,
 agent TEXT NOT NULL,
 model TEXT NOT NULL,
 effort TEXT NOT NULL,
 cwd TEXT NOT NULL
);
