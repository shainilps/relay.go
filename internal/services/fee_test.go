package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shainilps/relay/internal/broadcaster"
)

func policyService(t *testing.T, status int, body string) *RelayService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policy" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	pool := broadcaster.NewArcPool(broadcaster.NewArc("test", server.URL, ""))
	return NewRelayService(nil, &broadcaster.Broadcaster{Arc: pool}, &fakeQueue{}, nil)
}

func TestFeeRateFromPolicy(t *testing.T) {
	t.Cleanup(func() { setFeeRate(FeeRate{Satoshis: DEFAULT_FEE_SATOSHIS, Bytes: DEFAULT_FEE_BYTES}) })
	ctx := context.Background()

	if got := feeForSize(193); got != 20 {
		t.Fatalf("expected the default rate to charge 20 sats for 193 bytes, got %d", got)
	}

	r := policyService(t, http.StatusOK, `{"policy":{"miningFee":{"satoshis":1,"bytes":1000}}}`)
	if err := r.refreshFeeRate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := feeForSize(193); got != 1 {
		t.Fatalf("expected 1 sat per kB to charge 1 sat for 193 bytes, got %d", got)
	}
	if got := feeForSize(1001); got != 2 {
		t.Fatalf("expected the fee to round up, got %d", got)
	}
	if got := minChange(); got != 1 {
		t.Fatalf("expected min change to follow the rate, got %d", got)
	}

	bad := policyService(t, http.StatusOK, `{"policy":{"miningFee":{"satoshis":5,"bytes":0}}}`)
	if err := bad.refreshFeeRate(ctx); err == nil {
		t.Fatal("expected a zero-byte policy to be rejected")
	}
	down := policyService(t, http.StatusServiceUnavailable, "down")
	if err := down.refreshFeeRate(ctx); err == nil {
		t.Fatal("expected an unreachable arc to return an error")
	}
	if rate := currentFeeRate(); rate.Satoshis != 1 || rate.Bytes != 1000 {
		t.Fatalf("expected the last good rate to be kept, got %+v", rate)
	}
}
