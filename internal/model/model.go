package model

import (
	"database/sql"
	"time"
)

type Network string
type TransactionStatus string

const (
	MAIN Network = "MAIN"
	TEST Network = "TEST"
)

const (
	SYNCED   TransactionStatus = "SYNCED"
	UNSYNCED TransactionStatus = "UNSYNCED"
)

type FundingUTXO struct {
	UtxoID        string
	TxID          string
	Vout          uint32
	LockingScript string
	SpentTxID     sql.NullString
	CreatedAt time.Time
}

type Transaction struct {
	TxID      string
	TxHex     string
	Height    uint64
	Network   Network
	Status    TransactionStatus
	CreatedAt time.Time
}

type UTXO struct {
	UtxoID        string
	TxID          string
	Vout          uint32
	LockingScript string
}
