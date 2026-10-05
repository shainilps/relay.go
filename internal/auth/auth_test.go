package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

var (
	walletToken  = strings.Repeat("w", 40)
	indexerToken = strings.Repeat("i", 40)
)

func serve(t *testing.T, authenticator *Authenticator, configure func(r *http.Request)) (*httptest.ResponseRecorder, string) {
	t.Helper()
	client := ""
	handler := authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client = ClientFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodPost, "/fund-and-broadcast", nil)
	if configure != nil {
		configure(request)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder, client
}

func TestNewRejectsBadConfig(t *testing.T) {
	cases := map[string]Config{
		"mode missing":         {},
		"unknown mode":         {Mode: "jwt"},
		"token without tokens": {Mode: ModeToken},
		"short token":          {Mode: ModeToken, Tokens: []Token{{Name: "a", Token: "short"}}},
		"token without name":   {Mode: ModeToken, Tokens: []Token{{Token: walletToken}}},
		"duplicate token name": {Mode: ModeToken, Tokens: []Token{{Name: "a", Token: walletToken}, {Name: "a", Token: indexerToken}}},
		"basic without users":  {Mode: ModeBasic},
		"short password":       {Mode: ModeBasic, Users: []User{{Username: "ops", Password: "short"}}},
		"user without name":    {Mode: ModeBasic, Users: []User{{Password: "long-enough-password"}}},
	}
	for name, config := range cases {
		if _, err := New(config); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestTokenMode(t *testing.T) {
	authenticator, err := New(Config{Mode: ModeToken, Tokens: []Token{{Name: "wallet", Token: walletToken}, {Name: "indexer", Token: indexerToken}}})
	if err != nil {
		t.Fatal(err)
	}

	recorder, client := serve(t, authenticator, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+indexerToken) })
	if recorder.Code != http.StatusOK || client != "indexer" {
		t.Fatalf("expected indexer to be let through, got %d %q", recorder.Code, client)
	}

	recorder, _ = serve(t, authenticator, func(r *http.Request) { r.Header.Set("Authorization", "bearer "+walletToken) })
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the scheme to be case insensitive, got %d", recorder.Code)
	}

	for name, header := range map[string]string{
		"missing":      "",
		"wrong token":  "Bearer " + strings.Repeat("x", 40),
		"wrong scheme": "Basic " + walletToken,
		"no token":     "Bearer ",
		"prefix only":  "Bearer " + walletToken[:20],
	} {
		recorder, client := serve(t, authenticator, func(r *http.Request) {
			if header != "" {
				r.Header.Set("Authorization", header)
			}
		})
		if recorder.Code != http.StatusUnauthorized || client != "" {
			t.Fatalf("%s: expected 401, got %d %q", name, recorder.Code, client)
		}
		if !strings.HasPrefix(recorder.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Fatalf("%s: expected a Bearer challenge, got %q", name, recorder.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestBasicMode(t *testing.T) {
	authenticator, err := New(Config{Mode: ModeBasic, Users: []User{{Username: "ops", Password: "correct-horse-battery"}}})
	if err != nil {
		t.Fatal(err)
	}

	recorder, client := serve(t, authenticator, func(r *http.Request) { r.SetBasicAuth("ops", "correct-horse-battery") })
	if recorder.Code != http.StatusOK || client != "ops" {
		t.Fatalf("expected ops to be let through, got %d %q", recorder.Code, client)
	}

	for name, configure := range map[string]func(r *http.Request){
		"missing":        nil,
		"wrong password": func(r *http.Request) { r.SetBasicAuth("ops", "wrong-password-here") },
		"wrong user":     func(r *http.Request) { r.SetBasicAuth("admin", "correct-horse-battery") },
		"bearer instead": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+walletToken) },
	} {
		recorder, _ := serve(t, authenticator, configure)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", name, recorder.Code)
		}
		if !strings.HasPrefix(recorder.Header().Get("WWW-Authenticate"), "Basic") {
			t.Fatalf("%s: expected a Basic challenge, got %q", name, recorder.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestNoneMode(t *testing.T) {
	authenticator, err := New(Config{Mode: ModeNone})
	if err != nil {
		t.Fatal(err)
	}
	recorder, client := serve(t, authenticator, nil)
	if recorder.Code != http.StatusOK || client != "anonymous" {
		t.Fatalf("expected an open request, got %d %q", recorder.Code, client)
	}
}

func TestLoadFromConfig(t *testing.T) {
	viper.Set("auth", map[string]any{
		"mode":   "token",
		"tokens": []map[string]any{{"name": "wallet", "token": walletToken}},
	})
	t.Cleanup(func() { viper.Set("auth", nil) })

	authenticator, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	recorder, client := serve(t, authenticator, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+walletToken) })
	if recorder.Code != http.StatusOK || client != "wallet" {
		t.Fatalf("expected the configured token to work, got %d %q", recorder.Code, client)
	}

	viper.Set("auth", map[string]any{})
	if _, err := Load(); err == nil {
		t.Fatal("expected a missing auth.mode to fail")
	}
}
