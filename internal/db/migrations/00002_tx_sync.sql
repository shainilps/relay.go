-- +goose Up
-- +goose StatementBegin

CREATE TABLE transactions_new(
    tx_id TEXT PRIMARY KEY,
    tx_hex TEXT NOT NULL,
    network TEXT NOT NULL CHECK(network IN ('MAIN', 'TEST')),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','BROADCASTED','SYNCED','FAILED')),
    attempts INT NOT NULL DEFAULT 0,
    last_broadcast_at INTEGER,
    next_attempt_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    block_hash TEXT,
    block_height BIGINT DEFAULT 0,
    last_error TEXT,
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP),
    updated_at TEXT DEFAULT (CURRENT_TIMESTAMP)
);

INSERT INTO transactions_new (tx_id, tx_hex, network, status, attempts, block_height, created_at)
SELECT
    tx_id,
    tx_hex,
    network,
    CASE WHEN status = 'SYNCED' THEN 'SYNCED' ELSE 'BROADCASTED' END,
    1,
    height,
    created_at
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;

CREATE INDEX idx_transactions_due ON transactions(status, next_attempt_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE transactions_old(
    tx_id TEXT PRIMARY KEY,
    tx_hex TEXT NOT NULL,
    height BIGINT DEFAULT 0,
    network TEXT NOT NULL CHECK(network IN ('MAIN', 'TEST')),
    status TEXT DEFAULT 'UNSYNCED' CHECK (status IN ('UNSYNCED','SYNCED')),
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP)
);

INSERT INTO transactions_old (tx_id, tx_hex, height, network, status, created_at)
SELECT
    tx_id,
    tx_hex,
    block_height,
    network,
    CASE WHEN status = 'SYNCED' THEN 'SYNCED' ELSE 'UNSYNCED' END,
    created_at
FROM transactions;

DROP INDEX IF EXISTS idx_transactions_due;
DROP TABLE transactions;
ALTER TABLE transactions_old RENAME TO transactions;

-- +goose StatementEnd
