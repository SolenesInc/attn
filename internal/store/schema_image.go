package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"
	"time"
)

//go:embed schema.sqlite.gz
var committedSchemaImage []byte

const imageProfileID = "profile-00000000-0000-0000-0000-000000000000"

var imageTime = time.Unix(0, 0).UTC()

type schemaImage struct {
	image    []byte
	versions []int
}

var committedImage = sync.OnceValues(func() (schemaImage, error) {
	reader, err := gzip.NewReader(bytes.NewReader(committedSchemaImage))
	if err != nil {
		return schemaImage{}, fmt.Errorf("reading the committed schema image: %w", err)
	}
	image, err := io.ReadAll(reader)
	if err != nil {
		return schemaImage{}, fmt.Errorf("reading the committed schema image: %w", err)
	}
	var history migrationHistory
	err = withImage(image, func(db *sql.DB) error {
		var err error
		history, err = readMigrationHistory(db)
		return err
	})
	if err != nil {
		return schemaImage{}, fmt.Errorf("reading the committed schema image: %w", err)
	}
	return schemaImage{image: image, versions: slices.Sorted(maps.Keys(history.applied))}, nil
})

var replayedImages sync.Map

func migratedSchemaImage() ([]byte, error) {
	committed, err := committedImage()
	if err != nil {
		return nil, err
	}
	if slices.Equal(committed.versions, migrationVersions()) {
		return committed.image, nil
	}
	build, _ := replayedImages.LoadOrStore(LatestSchemaVersion(), sync.OnceValues(replaySchemaImage))
	return build.(func() ([]byte, error))()
}

func migrationVersions() []int {
	var versions []int
	for _, m := range allMigrations() {
		versions = append(versions, m.version)
	}
	return versions
}

func GenerateSchemaImage() ([]byte, error) {
	replayed, err := replaySchemaImage()
	if err != nil {
		return nil, err
	}
	var image []byte
	err = withImage(replayed, func(db *sql.DB) error {
		if err := stampIdentity(db, imageProfileID, imageTime); err != nil {
			return err
		}
		if _, err := db.Exec(`VACUUM`); err != nil {
			return err
		}
		image, err = serialize(db)
		return err
	})
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(image); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func replaySchemaImage() ([]byte, error) {
	db, _ := connectSQLite(":memory:")
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := upgradeSchema(db, ":memory:"); err != nil {
		return nil, err
	}
	return serialize(db)
}

func serialize(db *sql.DB) ([]byte, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var image []byte
	err = conn.Raw(func(driverConn any) error {
		var err error
		image, err = driverConn.(*sqliteConnection).Serialize("main")
		return err
	})
	return image, err
}

func withImage(image []byte, use func(db *sql.DB) error) error {
	db, _ := connectSQLite(":memory:")
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	err = conn.Raw(func(driverConn any) error {
		return driverConn.(*sqliteConnection).Deserialize(image, "main")
	})
	conn.Close()
	if err != nil {
		return err
	}
	return use(db)
}

func copyMigratedSchema(dst *sql.DB) error {
	image, err := migratedSchemaImage()
	if err != nil {
		return fmt.Errorf("building migrated schema: %w", err)
	}
	return withImage(image, func(src *sql.DB) error {
		if err := stampIdentity(src, newProfileEntityID("profile"), time.Now()); err != nil {
			return err
		}
		return backup(src, dst)
	})
}

func backup(src, dst *sql.DB) error {
	ctx := context.Background()
	srcConn, err := src.Conn(ctx)
	if err != nil {
		return err
	}
	defer srcConn.Close()
	dstConn, err := dst.Conn(ctx)
	if err != nil {
		return err
	}
	defer dstConn.Close()
	return srcConn.Raw(func(srcDriver any) error {
		return dstConn.Raw(func(dstDriver any) error {
			return copyDatabase(dstDriver.(*sqliteConnection).SQLiteConn, srcDriver.(*sqliteConnection).SQLiteConn)
		})
	})
}

func stampIdentity(db *sql.DB, profileID string, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var seeded string
	if err := tx.QueryRow(`SELECT id FROM profiles`).Scan(&seeded); err != nil {
		return fmt.Errorf("finding the seeded Default profile: %w", err)
	}
	stamp := now.UTC().Format(sortableTimeFormat)
	for _, statement := range []string{
		`UPDATE profiles SET id = :new, current_desktop_id = replace(current_desktop_id, :old, :new), created_at = :stamp`,
		`UPDATE desktops SET id = replace(id, :old, :new), profile_id = :new, created_at = :stamp, updated_at = :stamp`,
		`UPDATE profile_migration SET imported_groups = replace(imported_groups, :old, :new)`,
		`UPDATE schema_migrations SET applied_at = :applied`,
	} {
		if _, err := tx.Exec(statement,
			sql.Named("old", seeded), sql.Named("new", profileID), sql.Named("stamp", stamp),
			sql.Named("applied", now.UTC().Format(time.DateTime)),
		); err != nil {
			return fmt.Errorf("giving the new database its own identity: %w", err)
		}
	}
	return tx.Commit()
}
