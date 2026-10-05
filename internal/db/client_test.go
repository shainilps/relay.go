package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

func TestNetworksUseSeparateSchemas(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	viper.Set("db.url", baseURL)
	t.Cleanup(func() { viper.Set("db.url", "") })

	admin, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatal(err)
	}
	dropSchemas := func() {
		admin.Exec(`DROP SCHEMA IF EXISTS relay_main CASCADE`)
		admin.Exec(`DROP SCHEMA IF EXISTS relay_test CASCADE`)
	}
	dropSchemas()
	t.Cleanup(func() {
		dropSchemas()
		admin.Close()
	})

	ctx := context.Background()
	mainDB, err := NewClient(model.MAIN)
	if err != nil {
		t.Fatal(err)
	}
	defer mainDB.Close()
	testDB, err := NewClient(model.TEST)
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()

	if _, err := mainDB.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex) VALUES ('only-on-main', '00')`); err != nil {
		t.Fatal(err)
	}

	var txID string
	err = testDB.QueryRowContext(ctx, `SELECT tx_id FROM transactions WHERE tx_id = 'only-on-main'`).Scan(&txID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected the test network not to see main data, got %q %v", txID, err)
	}
	if err := mainDB.QueryRowContext(ctx, `SELECT tx_id FROM transactions WHERE tx_id = 'only-on-main'`).Scan(&txID); err != nil {
		t.Fatalf("expected main to see its own data, got %v", err)
	}

	again, err := NewClient(model.MAIN)
	if err != nil {
		t.Fatalf("expected reopening main to be a no-op migration, got %v", err)
	}
	again.Close()
}
