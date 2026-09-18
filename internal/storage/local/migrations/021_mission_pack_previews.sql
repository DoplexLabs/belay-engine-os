CREATE TABLE IF NOT EXISTS mission_pack_previews (
    pack_id TEXT PRIMARY KEY
        CHECK (length(pack_id) >= 1 AND length(pack_id) <= 512),
    expires_at TEXT NOT NULL,
    payload BLOB NOT NULL CHECK (length(payload) <= 263168),
    payload_encoding TEXT NOT NULL
        CHECK (payload_encoding = 'aes256gcm.v1'),
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS mission_pack_previews_expiry_idx
    ON mission_pack_previews(expires_at, pack_id);
