package ingest

import "context"

var schema = []string{
	`CREATE TABLE IF NOT EXISTS installs (
		install_id   String,
		token_sha256 String,
		ip_hash      String,
		created_at   DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY token_sha256`,
	`CREATE TABLE IF NOT EXISTS events (
		install_id        String,
		stream            LowCardinality(String),
		seq               Int64,
		name              LowCardinality(String),
		subject           String,
		at                DateTime64(3, 'UTC'),
		bytes             UInt32,
		props             String,
		build_version     LowCardinality(String),
		build_commit      LowCardinality(String),
		build_channel     LowCardinality(String),
		build_time        LowCardinality(String),
		build_fingerprint LowCardinality(String),
		build_protocol    LowCardinality(String),
		build_os          LowCardinality(String),
		build_arch        LowCardinality(String),
		build_dirty       Bool,
		app_version       LowCardinality(String),
		app_channel       LowCardinality(String),
		ip_hash           String,
		received_at       DateTime64(3, 'UTC')
	) ENGINE = ReplacingMergeTree
	PARTITION BY toYYYYMM(at)
	ORDER BY (install_id, stream, seq)`,
	`CREATE TABLE IF NOT EXISTS quarantine (
		install_id String,
		ip_hash    String,
		reason     String,
		at         DateTime64(3, 'UTC')
	) ENGINE = MergeTree ORDER BY (install_id, ip_hash)`,
}

func (c *clickHouse) applySchema(ctx context.Context) error {
	for _, statement := range schema {
		if err := c.exec(ctx, statement, nil); err != nil {
			return err
		}
	}
	return nil
}
