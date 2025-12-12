package broadcaster

type WOCUtxo struct {
	Height             uint64 `json:"height"`
	TxPos              uint32 `json:"tx_pos"`
	TxHash             string `json:"tx_hash"`
	Value              uint64 `json:"value"`
	IsSpentInMempoolTx bool   `json:"isSpentInMempoolTx"`
}

type WOCUtxoResponse struct {
	Address string    `json:"address"`
	Script  string    `json:"script"`
	Result  []WOCUtxo `json:"result"`
	Error   string    `json:"error"`
}
