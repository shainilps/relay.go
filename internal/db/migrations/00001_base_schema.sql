-- +goose Up
-- +goose StatementBegin

CREATE TABLE transactions(
    txid TEXT PRIMARY KEY,
    txhex TEXT,
    height BIGINT,
    network TEXT CHECK(network IN ('main', 'test'))
    status TEXT DEFAULT 'UNSYNCED' CHECK (status IN ('UNSYNCED','SYNCED'))
);

CREATE TABLE utxos(
    utxo_id TEXT PRIMARY KEY,
    txid TEXT REFERENCES transactions(txid),
    vout INT,
    locking_script TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE transactions;
DROP TABLE utxos;
-- +goose StatementEnd
