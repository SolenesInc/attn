UPDATE sessions SET closed_by = 'session:' || closed_by WHERE closed_by NOT IN ('', 'user');
