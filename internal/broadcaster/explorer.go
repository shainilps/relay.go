package broadcaster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shainilps/relay/internal/model"
)

const (
	WOCURL = "https://api.whatsonchain.com/v1/bsv"
)

type WOCExplorer struct {
	network model.Network
	token   string
}

func NewWOCExplorerProvider(network model.Network, token string) *WOCExplorer {
	return &WOCExplorer{
		network: network,
		token:   token,
	}
}

func (w *WOCExplorer) GetUtxosForAddress(ctx context.Context, address string) (*WOCUtxoResponse, error) {

	if address == "" {
		return nil, fmt.Errorf("address cannot be  empty")
	}

	// GET https://api.whatsonchain.com/v1/bsv/<network>/address/<address>/unspent/all
	url := fmt.Sprintf("%s/%s/address/%s/unspent/all", WOCURL, w.network, address)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetUtxosForAddress: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result WOCUtxoResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.Error != "" {
		return nil, fmt.Errorf("WOC API error: %s", result.Error)
	}

	return &result, nil
}
