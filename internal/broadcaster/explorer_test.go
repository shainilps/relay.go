package broadcaster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shainilps/relay/internal/model"
)

func TestGetOutputSpent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main/tx/spent/0/spent":
			w.Write([]byte(`{"txid":"spender","vin":1,"status":"confirmed"}`))
		case "/main/tx/unspent/0/spent":
			w.WriteHeader(http.StatusNotFound)
		case "/main/tx/unknown/0/spent":
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	explorer := NewWOCExplorer(server.URL, model.MAIN, "")
	ctx := context.Background()

	status, spentBy, err := explorer.GetOutputSpent(ctx, "spent", 0)
	if err != nil || status != OutputSpent || spentBy != "spender" {
		t.Fatalf("expected spent by spender, got %v %q %v", status, spentBy, err)
	}
	status, _, err = explorer.GetOutputSpent(ctx, "unspent", 0)
	if err != nil || status != OutputUnspent {
		t.Fatalf("expected unspent, got %v %v", status, err)
	}
	status, _, err = explorer.GetOutputSpent(ctx, "unknown", 0)
	if err != nil || status != OutputUnknown {
		t.Fatalf("expected unknown, got %v %v", status, err)
	}
	if _, _, err = explorer.GetOutputSpent(ctx, "broken", 0); err == nil {
		t.Fatal("expected an error on a server error")
	}
}
