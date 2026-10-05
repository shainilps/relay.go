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

type Arc struct {
	name  string
	url   string
	token string
}

const (
	TaalMainURL        = "https://arc.taal.com/v1"
	TaalTestURL        = "https://arc-test.taal.com/v1"
	GorillaPoolMainURL = "https://arc.gorillapool.io/v1"
)

func NewArc(name string, url string, token string) *Arc {
	return &Arc{name: name, url: url, token: token}
}

func NewTaalArcProvider(network model.Network, token string) *Arc {
	url := TaalMainURL
	if network == model.TEST {
		url = TaalTestURL
	}
	return NewArc("taal", url, token)
}

func (t *Arc) Name() string {
	return t.name
}

func (t *Arc) setAuth(req *http.Request) {
	if t.token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.token))
	}
}

func (t *Arc) GetPolicy(ctx context.Context) (*PolicyResponse, error) {
	url := fmt.Sprintf("%s/policy", t.url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, &ArcError{StatusCode: resp.StatusCode, Body: string(bodyBytes)}
	}

	var pr PolicyResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (t *Arc) BroadcastTx(ctx context.Context, txHex string, headers map[string]string) (*BroadcastTxResponse, error) {
	url := fmt.Sprintf("%s/tx", t.url)
	body := bytes.NewBufferString(txHex)
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)
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
		return nil, &ArcError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var br BroadcastTxResponse
	if err := json.Unmarshal(respBody, &br); err != nil {
		return nil, err
	}
	return &br, nil
}

func (t *Arc) GetTxStatus(ctx context.Context, txid string) (*TxStatusResponse, error) {
	url := fmt.Sprintf("%s/tx/%s", t.url, txid)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &ArcError{StatusCode: resp.StatusCode, Body: string(bodyBytes)}
	}

	var tr TxStatusResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

func (t *Arc) GetHealth(ctx context.Context) (*HealthResponse, error) {
	url := fmt.Sprintf("%s/health", t.url)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	t.setAuth(req)
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
