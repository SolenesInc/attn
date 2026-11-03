CREATE INDEX idx_analytics_outbox_unsent ON analytics_outbox(id) WHERE sent_at IS NULL;
