ALTER TABLE update_run_outposts ADD COLUMN role TEXT NOT NULL DEFAULT 'outpost';
ALTER TABLE update_run_outposts ADD COLUMN build TEXT NOT NULL DEFAULT '';
ALTER TABLE update_run_outposts ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE update_run_outposts ADD COLUMN action TEXT NOT NULL DEFAULT '';
