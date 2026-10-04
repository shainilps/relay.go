package model

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
