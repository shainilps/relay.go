-- +goose Up
-- +goose StatementBegin

CREATE TABLE queue_utxos(
    utxo_id TEXT PRIMARY KEY,
    tx_id TEXT NOT NULL,
    vout INT NOT NULL,
    amount INT NOT NULL,
    queue TEXT NOT NULL,
    published BOOL NOT NULL DEFAULT FALSE,
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP)
);

CREATE INDEX idx_queue_utxos_unpublished ON queue_utxos(published);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE queue_utxos;

-- +goose StatementEnd
