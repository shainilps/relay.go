package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shainilps/relay/internal/auth"
	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/services"
)

func TestGetTransaction(t *testing.T) {
	db := dbtest.New(t)
	txID := strings.Repeat("ab", 32)
	if err := repo.CreateTransaction(context.Background(), db, &model.Transaction{TxID: txID, TxHex: "00"}, nil); err != nil {
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
	if tx.TxID != txID || tx.Status != model.PENDING {
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

func TestRouterProtectsEverythingButHealth(t *testing.T) {
	authenticator, err := auth.New(auth.Config{Mode: auth.ModeToken, Tokens: []auth.Token{{Name: "wallet", Token: strings.Repeat("w", 40)}}})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(NewHandler(services.NewRelayService(nil, nil, nil, nil)), authenticator.Middleware)

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("expected /health to be open, got %d", health.Code)
	}

	for _, path := range []string{"/broadcast", "/fund-and-broadcast", "/funding-address", "/tx?txid=" + strings.Repeat("ab", 32), "/unknown"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("expected %s to need auth, got %d", path, recorder.Code)
		}
	}

	authorized := httptest.NewRequest(http.MethodPost, "/tx", nil)
	authorized.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 40))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorized)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected an authorized request to reach the handler, got %d", recorder.Code)
	}
}
