package store

import "fmt"

func (s *Store) PluginServerOwners() (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	owners := make(map[string]string)
	rows, err := s.db.Query(`SELECT agent, plugin_name FROM plugin_server_owners`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var agent, plugin string
		if err := rows.Scan(&agent, &plugin); err != nil {
			return nil, err
		}
		owners[agent] = plugin
	}
	return owners, rows.Err()
}

func (s *Store) ClaimPluginServer(agent, plugin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO plugin_server_owners (agent, plugin_name) VALUES (?, ?) ON CONFLICT(agent) DO NOTHING`, agent, plugin)
	if err != nil {
		return err
	}
	var owner string
	if err := s.db.QueryRow(`SELECT plugin_name FROM plugin_server_owners WHERE agent = ?`, agent).Scan(&owner); err != nil {
		return err
	}
	if owner != plugin {
		return fmt.Errorf("agent %q belongs to plugin %q", agent, owner)
	}
	return nil
}

func (s *Store) DeletePluginServerOwners(plugin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM plugin_server_owners WHERE plugin_name = ?`, plugin)
	return err
}
