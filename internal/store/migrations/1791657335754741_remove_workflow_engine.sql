DROP TABLE workflow_agent_calls;
DROP TABLE workflow_runs;
DELETE FROM settings WHERE key = 'workflows_enabled';
