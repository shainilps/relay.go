-- +goose Up
-- +goose StatementBegin

CREATE TABLE transactions(
    seq BIGSERIAL NOT NULL UNIQUE,
    tx_id TEXT PRIMARY KEY,
    tx_hex TEXT NOT NULL,
    network TEXT NOT NULL CHECK(network IN ('MAIN', 'TEST')),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','BROADCASTED','SYNCED','FAILED')),
    attempts INT NOT NULL DEFAULT 0,
    last_broadcast_at BIGINT,
    next_attempt_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM now())::BIGINT,
    block_hash TEXT,
    block_height BIGINT DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_transactions_due ON transactions(status, next_attempt_at);

CREATE TABLE tx_inputs(
    prev_tx_id TEXT NOT NULL,
    vout BIGINT NOT NULL,
    tx_id TEXT NOT NULL REFERENCES transactions(tx_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (prev_tx_id, vout)
);

CREATE INDEX idx_tx_inputs_tx_id ON tx_inputs(tx_id);

CREATE TABLE funding_utxos(
    utxo_id TEXT PRIMARY KEY,
    tx_id TEXT NOT NULL,
    vout BIGINT NOT NULL,
    amount BIGINT NOT NULL,
    is_spent BOOLEAN NOT NULL DEFAULT FALSE,
    chain_spent BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE queue_utxos(
    seq BIGSERIAL NOT NULL UNIQUE,
    utxo_id TEXT PRIMARY KEY,
    tx_id TEXT NOT NULL,
    vout BIGINT NOT NULL,
    amount BIGINT NOT NULL,
    queue TEXT NOT NULL,
    published BOOLEAN NOT NULL DEFAULT FALSE,
    chain_spent BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tx_id, vout)
);

CREATE INDEX idx_queue_utxos_unpublished ON queue_utxos(published);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE queue_utxos;
DROP TABLE funding_utxos;
DROP TABLE tx_inputs;
DROP TABLE transactions;

-- +goose StatementEnd
