package broadcaster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shainilps/relay/internal/model"
)

const (
	WOCURL = "https://api.whatsonchain.com/v1/bsv"
)

type WOCExplorer struct {
	baseURL string
	network model.Network
	token   string
}

func NewWOCExplorerProvider(network model.Network, token string) *WOCExplorer {
	return NewWOCExplorer(WOCURL, network, token)
}

func NewWOCExplorer(baseURL string, network model.Network, token string) *WOCExplorer {
	return &WOCExplorer{
		baseURL: baseURL,
		network: network,
		token:   token,
	}
}

func (w *WOCExplorer) GetUtxosForAddress(ctx context.Context, address string) (*WOCUtxoResponse, error) {

	if address == "" {
		return nil, fmt.Errorf("address cannot be  empty")
	}

	// GET https://api.whatsonchain.com/v1/bsv/<network>/address/<address>/unspent/all
	url := fmt.Sprintf("%s/%s/address/%s/unspent/all", w.baseURL, strings.ToLower(string(w.network)), address)

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

type OutputSpentStatus int

const (
	OutputUnspent OutputSpentStatus = iota
	OutputSpent
	OutputUnknown
)

func (w *WOCExplorer) GetOutputSpent(ctx context.Context, txid string, vout uint32) (OutputSpentStatus, string, error) {

	url := fmt.Sprintf("%s/%s/tx/%s/%d/spent", w.baseURL, strings.ToLower(string(w.network)), txid, vout)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return OutputUnknown, "", err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return OutputUnknown, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OutputUnknown, "", err
	}

	switch resp.StatusCode {
	case http.StatusNotFound:
		return OutputUnspent, "", nil
	case http.StatusBadRequest:
		return OutputUnknown, "", nil
	case http.StatusOK:
		var spent WOCSpentResponse
		if err := json.Unmarshal(body, &spent); err != nil {
			return OutputUnknown, "", err
		}
		return OutputSpent, spent.TxID, nil
	default:
		return OutputUnknown, "", fmt.Errorf("GetOutputSpent: unexpected status %d: %s", resp.StatusCode, string(body))
	}
}
