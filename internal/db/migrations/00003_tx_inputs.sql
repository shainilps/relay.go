-- +goose Up
-- +goose StatementBegin

CREATE TABLE tx_inputs(
    prev_tx_id TEXT NOT NULL,
    vout INT NOT NULL,
    tx_id TEXT NOT NULL,
    created_at TEXT DEFAULT (CURRENT_TIMESTAMP),
    PRIMARY KEY (prev_tx_id, vout)
);

CREATE INDEX idx_tx_inputs_tx_id ON tx_inputs(tx_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE tx_inputs;

-- +goose StatementEnd
