ALTER TABLE outbox ADD COLUMN dedupe_key TEXT;
CREATE UNIQUE INDEX uq_outbox_dedupe ON outbox(dedupe_key, channel, target) WHERE dedupe_key IS NOT NULL;
