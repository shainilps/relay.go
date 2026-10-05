package model

type Network string
type TransactionStatus string

const (
	MAIN Network = "MAIN"
	TEST Network = "TEST"
)

const (
	PENDING     TransactionStatus = "PENDING"
	BROADCASTED TransactionStatus = "BROADCASTED"
	SYNCED      TransactionStatus = "SYNCED"
	FAILED      TransactionStatus = "FAILED"
)

type UTXO struct {
	UtxoID string
	TxID   string
	Vout   uint32
	Amount uint64
}

type QueueUTXO struct {
	UTXO
	Queue string
}

type Outpoint struct {
	TxID string
	Vout uint32
}

type Transaction struct {
	TxID            string            `json:"txid"`
	TxHex           string            `json:"-"`
	Status          TransactionStatus `json:"status"`
	Attempts        int               `json:"attempts"`
	LastBroadcastAt *int64            `json:"lastBroadcastAt,omitempty"`
	NextAttemptAt   int64             `json:"-"`
	BlockHash       string            `json:"blockHash,omitempty"`
	BlockHeight     uint64            `json:"blockHeight,omitempty"`
	LastError       string            `json:"lastError,omitempty"`
	CreatedAt       int64             `json:"createdAt"`
}
