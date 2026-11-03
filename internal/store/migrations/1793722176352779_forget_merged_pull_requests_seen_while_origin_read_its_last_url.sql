DELETE FROM repo_merged_branches
WHERE julianday(observed_at) >= julianday('2026-11-03T00:00:00Z');

DELETE FROM repo_integration_branches
WHERE source = 'pull_requests' AND julianday(resolved_at) >= julianday('2026-11-03T00:00:00Z');
