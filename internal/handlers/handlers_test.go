package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/services"
)

func TestGetTransaction(t *testing.T) {
	db := dbtest.New(t)
	txID := strings.Repeat("ab", 32)
	if err := repo.CreateTransaction(context.Background(), db, &model.Transaction{TxID: txID, TxHex: "00", Network: model.TEST}, nil); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(services.NewRelayService(db, nil, nil, nil))

	request := func(method string, target string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.GetTransaction(recorder, httptest.NewRequest(method, target, nil))
		return recorder
	}

	found := request(http.MethodGet, "/tx?txid="+txID)
	if found.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", found.Code, found.Body.String())
	}
	var tx model.Transaction
	if err := json.Unmarshal(found.Body.Bytes(), &tx); err != nil {
		t.Fatal(err)
	}
	if tx.TxID != txID || tx.Status != model.PENDING || tx.Network != model.TEST {
		t.Fatalf("unexpected body %+v", tx)
	}

	for target, expected := range map[string]int{
		"/tx?txid=" + strings.Repeat("cd", 32): http.StatusNotFound,
		"/tx?txid=nothex":                      http.StatusBadRequest,
		"/tx":                                  http.StatusBadRequest,
	} {
		if got := request(http.MethodGet, target).Code; got != expected {
			t.Fatalf("expected %d for %s, got %d", expected, target, got)
		}
	}

	if got := request(http.MethodPost, "/tx?txid="+txID).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", got)
	}
}
