package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/garden"
)

func migrateGardenDispatchWatches(tx *sql.Tx) error {
	_, dispatchTable, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionDispatches)
	if err != nil || !found {
		return err
	}
	_, seedTable, found, err := readCollectionTx(tx, garden.Namespace, garden.CollectionSeeds)
	if err != nil || !found {
		return err
	}
	rows, err := tx.Query(`SELECT body FROM ` + dispatchTable)
	if err != nil {
		return err
	}
	var watches []GardenSeedWatch
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		dispatch, err := garden.DecodeDispatch(body)
		if err != nil {
			rows.Close()
			return fmt.Errorf("decode dispatch subscription: %w", err)
		}
		if dispatch.DispatcherSession != "" && dispatch.Crown != "" {
			watches = append(watches, GardenSeedWatch{WatcherSessionID: dispatch.DispatcherSession, SeedID: dispatch.Crown})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, watch := range watches {
		_, err := tx.Exec(`INSERT OR IGNORE INTO garden_seed_watches(watcher_session_id, seed_id, created_at)
   SELECT ?, id, ? FROM `+seedTable+` WHERE id = ?`, watch.WatcherSessionID, time.Now().UTC().Format(sortableTimeFormat), watch.SeedID)
		if err != nil {
			return fmt.Errorf("convert dispatch subscription: %w", err)
		}
	}
	return nil
}
