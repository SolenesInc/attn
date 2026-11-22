CREATE TABLE adapter_terminals (
    terminal_id TEXT PRIMARY KEY,
    owner TEXT NOT NULL
);

INSERT INTO adapter_terminals (terminal_id, owner) VALUES ('sigma-server', 'appserver');
INSERT OR IGNORE INTO adapter_terminals (terminal_id, owner) SELECT agent || '-server', 'appserver' FROM plugin_server_owners;
