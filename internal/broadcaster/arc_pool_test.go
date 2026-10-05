package broadcaster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func arcServer(t *testing.T, status int, body string, hits *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestArcPoolFallsBackWhenUnreachable(t *testing.T) {
	var primaryHits, fallbackHits int
	primary := arcServer(t, http.StatusBadGateway, "down", &primaryHits)
	fallback := arcServer(t, http.StatusOK, `{"txid":"abc","txStatus":"SEEN_ON_NETWORK"}`, &fallbackHits)

	pool := NewArcPool(NewArc("primary", primary.URL, "token"), NewArc("fallback", fallback.URL, ""))
	response, err := pool.BroadcastTx(context.Background(), "00", nil)
	if err != nil {
		t.Fatalf("expected the fallback to accept, got %v", err)
	}
	if response.Txid != "abc" || primaryHits != 1 || fallbackHits != 1 {
		t.Fatalf("unexpected result %+v primary %d fallback %d", response, primaryHits, fallbackHits)
	}
}

func TestArcPoolDoesNotFallBackOnRejection(t *testing.T) {
	var primaryHits, fallbackHits int
	primary := arcServer(t, 465, `{"detail":"fee too low"}`, &primaryHits)
	fallback := arcServer(t, http.StatusOK, `{"txid":"abc"}`, &fallbackHits)

	pool := NewArcPool(NewArc("primary", primary.URL, "token"), NewArc("fallback", fallback.URL, ""))
	_, err := pool.BroadcastTx(context.Background(), "00", nil)
	if err == nil || IsUnreachable(err) {
		t.Fatalf("expected the rejection to be returned, got %v", err)
	}
	if fallbackHits != 0 {
		t.Fatalf("expected no fallback on a rejection, got %d hits", fallbackHits)
	}
}

func TestArcPoolAllUnreachable(t *testing.T) {
	var primaryHits, fallbackHits int
	primary := arcServer(t, http.StatusServiceUnavailable, "down", &primaryHits)
	fallback := arcServer(t, http.StatusServiceUnavailable, "down", &fallbackHits)

	pool := NewArcPool(NewArc("primary", primary.URL, ""), NewArc("fallback", fallback.URL, ""))
	_, err := pool.GetTxStatus(context.Background(), "abc")
	if !IsUnreachable(err) || primaryHits != 1 || fallbackHits != 1 {
		t.Fatalf("expected unreachable after trying both, got %v primary %d fallback %d", err, primaryHits, fallbackHits)
	}
}
