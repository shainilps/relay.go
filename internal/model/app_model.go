package model

type Network string
type TransactionStatus string

const (
	MAIN Network = "main"
	TEST Network = "test"
)

const (
	SYNCED   TransactionStatus = "synced"
	UNSYNCED TransactionStatus = "unsynced"
)

type UTXO struct {
	UtxoID string
	TxID   string
	Vout   uint32
	Amount uint64
}

type Transaction struct {
	TxID    string
	TxHex   string
	Height  uint64
	Network Network
	Status  TransactionStatus
}
