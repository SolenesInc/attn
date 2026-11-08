ALTER TABLE automation_runs ADD COLUMN fix_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE automation_runs ADD COLUMN fix_directory TEXT NOT NULL DEFAULT '';
