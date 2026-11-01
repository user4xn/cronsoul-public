-- Channels own their notification policy: each channel decides which run events
-- it broadcasts, so endpoints no longer carry per-job notification settings.
ALTER TABLE channels ADD COLUMN events_json TEXT;
