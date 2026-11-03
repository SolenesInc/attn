CREATE INDEX idx_delegation_operations_reserved ON delegation_operations(session_id) WHERE state IN ('accepted', 'preparing');
