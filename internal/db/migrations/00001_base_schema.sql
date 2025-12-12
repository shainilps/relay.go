-- +goose Up
-- +goose StatementBegin

-- gonna keep track of all the transaction that go through here 
CREATE TABLE transactions(
    tx_id TEXT PRIMARY KEY,
    tx_hex TEXT NOT NULL,
    height BIGINT DEFAULT 0,
    network TEXT NOT NULL CHECK(network IN ('MAIN', 'TEST')),
    status TEXT DEFAULT 'UNSYNCED' CHECK (status IN ('UNSYNCED','SYNCED')),
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP)
);

-- only store funding utxo for queue
CREATE TABLE funding_utxos(
    utxo_id TEXT PRIMARY KEY,
    tx_id TEXT NOT NULL,  
    vout INT NOT NULL,
    amount INT NOT NULL, 
    is_spent BOOL DEFAULT FALSE,
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- DROP TABLE transactions; 
-- DROP TABLE utxos;

-- +goose StatementEnd
