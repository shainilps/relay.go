package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/shainilps/relay/internal/model"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	files, err := filepath.Glob("../migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(content), "-- +goose Down")
		if _, err := db.Exec(up); err != nil {
			t.Fatalf("migration %s: %v", file, err)
		}
	}
	return db
}

func TestTransactionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	tx := &model.Transaction{TxID: "tx1", TxHex: "00", Network: model.TEST, NextAttemptAt: 100}
	if err := CreateTransaction(ctx, db, tx, nil); err != nil {
		t.Fatal(err)
	}
	if err := MarkBroadcasted(ctx, db, "tx1", 50, 200); err != nil {
		t.Fatal(err)
	}
	if err := CreateTransaction(ctx, db, tx, nil); err != nil {
		t.Fatal(err)
	}

	got, err := GetTransaction(ctx, db, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.BROADCASTED || got.Attempts != 1 || got.LastBroadcastAt == nil || *got.LastBroadcastAt != 50 || got.NextAttemptAt != 200 || got.CreatedAt == 0 {
		t.Fatalf("unexpected row after broadcast: %+v", got)
	}

	due, err := GetDueTransactions(ctx, db, 199, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected nothing due before next_attempt_at, got %d", len(due))
	}
	due, err = GetDueTransactions(ctx, db, 200, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due tx, got %d", len(due))
	}

	if err := RecordBroadcastError(ctx, db, "tx1", "arc down", 300); err != nil {
		t.Fatal(err)
	}
	got, err = GetTransaction(ctx, db, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.BROADCASTED || got.Attempts != 2 || got.LastError != "arc down" {
		t.Fatalf("unexpected row after broadcast error: %+v", got)
	}

	if err := MarkSynced(ctx, db, "tx1", "blockhash", 42); err != nil {
		t.Fatal(err)
	}
	got, err = GetTransaction(ctx, db, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.SYNCED || got.BlockHash != "blockhash" || got.BlockHeight != 42 || got.LastError != "" {
		t.Fatalf("unexpected row after sync: %+v", got)
	}
	due, err = GetDueTransactions(ctx, db, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected synced tx not to be due, got %d", len(due))
	}
}

func TestMarkFailed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "tx1", TxHex: "00", Network: model.MAIN}, nil); err != nil {
		t.Fatal(err)
	}
	if err := MarkFailed(ctx, db, "tx1", "not mined"); err != nil {
		t.Fatal(err)
	}
	got, err := GetTransaction(ctx, db, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.FAILED || got.LastError != "not mined" {
		t.Fatalf("unexpected row: %+v", got)
	}
}

func TestRecordUnreachable(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "tx1", TxHex: "00", Network: model.MAIN, NextAttemptAt: 500}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordUnreachable(ctx, db, "tx1", "arc status 503", 100); err != nil {
		t.Fatal(err)
	}
	got, err := GetTransaction(ctx, db, "tx1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.PENDING || got.Attempts != 0 || got.NextAttemptAt != 100 || got.LastError != "arc status 503" {
		t.Fatalf("unexpected row: %+v", got)
	}
}

func TestDoubleSpend(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	parentOutput := model.Outpoint{TxID: "parent", Vout: 0}
	otherOutput := model.Outpoint{TxID: "parent", Vout: 1}

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "child1", TxHex: "00", Network: model.MAIN}, []model.Outpoint{parentOutput}); err != nil {
		t.Fatal(err)
	}

	t.Run("retrying the same tx is fine", func(t *testing.T) {
		if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "child1", TxHex: "00", Network: model.MAIN}, []model.Outpoint{parentOutput}); err != nil {
			t.Fatalf("expected idempotent insert, got %v", err)
		}
	})

	t.Run("spending a claimed output is a double spend", func(t *testing.T) {
		err := CreateTransaction(ctx, db, &model.Transaction{TxID: "child2", TxHex: "00", Network: model.MAIN}, []model.Outpoint{otherOutput, parentOutput})
		var doubleSpendErr *DoubleSpendError
		if !errors.As(err, &doubleSpendErr) || doubleSpendErr.SpentBy != "child1" {
			t.Fatalf("expected double spend by child1, got %v", err)
		}
		if _, err := GetTransaction(ctx, db, "child2"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected the rejected tx not to be stored, got %v", err)
		}
		var owners int
		if err := db.QueryRow(`SELECT COUNT(*) FROM tx_inputs WHERE tx_id = 'child2'`).Scan(&owners); err != nil {
			t.Fatal(err)
		}
		if owners != 0 {
			t.Fatalf("expected no inputs claimed by the rejected tx, got %d", owners)
		}
	})

	t.Run("a failed tx releases its inputs", func(t *testing.T) {
		if err := MarkFailed(ctx, db, "child1", "rejected"); err != nil {
			t.Fatal(err)
		}
		if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "child3", TxHex: "00", Network: model.MAIN}, []model.Outpoint{parentOutput}); err != nil {
			t.Fatalf("expected the output of a failed tx to be spendable, got %v", err)
		}
	})
}
