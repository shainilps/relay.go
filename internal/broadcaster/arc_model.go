package broadcaster

type PolicyResponse struct {
	Timestamp string `json:"timestamp"`
	Policy    struct {
		MaxScriptSizePolicy     uint64 `json:"maxscriptsizepolicy"`
		MaxTxSigOpsCountsPolicy uint64 `json:"maxtxsigopscountspolicy"`
		MaxTxSizePolicy         uint64 `json:"maxtxsizepolicy"`
		MiningFee               struct {
			Satoshis uint64 `json:"satoshis"`
			Bytes    uint64 `json:"bytes"`
		} `json:"miningFee"`
		StandardFormatSupported bool `json:"standardFormatSupported"`
	} `json:"policy"`
}

type BroadcastTxResponse struct {
	BlockHash   string `json:"blockHash,omitempty"`
	BlockHeight uint64 `json:"blockHeight,omitempty"`
	Txid        string `json:"txid,omitempty"`
	Status      int    `json:"status,omitempty"`
	TxStatus    string `json:"txStatus,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	Title       string `json:"title,omitempty"`
	ExtraInfo   string `json:"extraInfo,omitempty"`
}

type TxStatusResponse struct {
	Timestamp    string     `json:"timestamp"`
	BlockHash    string     `json:"blockHash,omitempty"`
	BlockHeight  uint64     `json:"blockHeight,omitempty"`
	Txid         string     `json:"txid"`
	MerklePath   string     `json:"merklePath,omitempty"`
	TxStatus     string     `json:"txStatus"`
	ExtraInfo    string     `json:"extraInfo,omitempty"`
	CompetingTxs [][]string `json:"competingTxs,omitempty"`
}

type HealthResponse struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
	Reason  string `json:"reason,omitempty"`
}
