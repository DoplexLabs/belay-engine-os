ALTER TABLE transcript_turns
ADD COLUMN price_table_version TEXT;

UPDATE transcript_turns
SET price_table_version = 'belay.local.prices.v2';

CREATE TABLE transcript_pricing_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    price_table_version TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO transcript_pricing_metadata (
    singleton,
    price_table_version,
    updated_at
) VALUES (
    1,
    'belay.local.prices.v2',
    CURRENT_TIMESTAMP
);
