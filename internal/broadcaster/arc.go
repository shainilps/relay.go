package broadcaster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shainilps/relay/internal/model"
)

const (
	TaalURL = "https://arc.taal.com/v1"
)

type TaalArc struct {
	network model.Network
	token   string
}

func NewTaalArcProvider(network model.Network, token string) *TaalArc {
	return &TaalArc{
		network,
		token,
	}
}

func (t *TaalArc) GetPolicy(ctx context.Context) (*PolicyResponse, error) {
	url := fmt.Sprintf("%s/policy", TaalURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.token))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetPolicy: unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var pr PolicyResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (t *TaalArc) BroadcastTx(ctx context.Context, txHex string, headers map[string]string) (*BroadcastTxResponse, error) {
	url := fmt.Sprintf("%s/tx", TaalURL)
	body := bytes.NewBufferString(txHex)
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.token))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BroadcastTx: status %d: %s", resp.StatusCode, string(respBody))
	}

	var br BroadcastTxResponse
	if err := json.Unmarshal(respBody, &br); err != nil {
		return nil, err
	}
	return &br, nil
}

func (t *TaalArc) GetTxStatus(ctx context.Context, txid string) (*TxStatusResponse, error) {
	url := fmt.Sprintf("%s/tx/%s", TaalURL, txid)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.token))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetTxStatus: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tr TxStatusResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

func (t *TaalArc) GetHealth(ctx context.Context) (*HealthResponse, error) {
	url := fmt.Sprintf("%s/health", TaalURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.token))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetHealth: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var hr HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return nil, err
	}
	return &hr, nil
}
