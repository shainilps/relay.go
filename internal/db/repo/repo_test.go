package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/model"
)

func TestTransactionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

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
	db := dbtest.New(t)

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
	db := dbtest.New(t)

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
	db := dbtest.New(t)

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

func TestGetSpendingTransaction(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	feeUtxo := model.Outpoint{TxID: "funding", Vout: 3}

	spentBy, err := GetSpendingTransaction(ctx, db, feeUtxo)
	if err != nil {
		t.Fatal(err)
	}
	if spentBy != "" {
		t.Fatalf("expected unspent, got spent by %s", spentBy)
	}

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "tx1", TxHex: "00", Network: model.MAIN}, []model.Outpoint{feeUtxo}); err != nil {
		t.Fatal(err)
	}
	spentBy, err = GetSpendingTransaction(ctx, db, feeUtxo)
	if err != nil {
		t.Fatal(err)
	}
	if spentBy != "tx1" {
		t.Fatalf("expected spent by tx1, got %q", spentBy)
	}

	if err := MarkFailed(ctx, db, "tx1", "rejected"); err != nil {
		t.Fatal(err)
	}
	spentBy, err = GetSpendingTransaction(ctx, db, feeUtxo)
	if err != nil {
		t.Fatal(err)
	}
	if spentBy != "" {
		t.Fatalf("expected a failed tx not to count as spending, got %s", spentBy)
	}
}

func TestStoreFundingTransaction(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	fundingInput := model.UTXO{UtxoID: "old_0", TxID: "old", Vout: 0, Amount: 10000}
	if err := CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{fundingInput}); err != nil {
		t.Fatal(err)
	}

	queueUtxos := []model.QueueUTXO{
		{UTXO: model.UTXO{UtxoID: "fund_0", TxID: "fund", Vout: 0, Amount: 50}, Queue: "QUEUE_50"},
		{UTXO: model.UTXO{UtxoID: "fund_1", TxID: "fund", Vout: 1, Amount: 100}, Queue: "QUEUE_100"},
	}
	change := &model.UTXO{UtxoID: "fund_2", TxID: "fund", Vout: 2, Amount: 9820}

	err := StoreFundingTransaction(ctx, db, &model.Transaction{TxID: "fund", TxHex: "00", Network: model.MAIN}, []model.UTXO{fundingInput}, queueUtxos, change)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := GetTransaction(ctx, db, "fund")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.PENDING {
		t.Fatalf("expected funding tx stored as PENDING, got %s", stored.Status)
	}

	spentBy, err := GetSpendingTransaction(ctx, db, model.Outpoint{TxID: "old", Vout: 0})
	if err != nil {
		t.Fatal(err)
	}
	if spentBy != "fund" {
		t.Fatalf("expected the funding input claimed by fund, got %q", spentBy)
	}

	unspent, err := GetAllUnspentFundingUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unspent) != 1 || unspent[0].UtxoID != "fund_2" {
		t.Fatalf("expected only the change to be unspent funding, got %+v", unspent)
	}

	unpublished, err := GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 2 || unpublished[0].UtxoID != "fund_0" || unpublished[0].Queue != "QUEUE_50" || unpublished[1].Amount != 100 {
		t.Fatalf("expected both queue utxos unpublished in order, got %+v", unpublished)
	}

	if err := MarkQueueUTXOPublished(ctx, db, "fund_0"); err != nil {
		t.Fatal(err)
	}
	unpublished, err = GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 1 || unpublished[0].UtxoID != "fund_1" {
		t.Fatalf("expected only fund_1 left to publish, got %+v", unpublished)
	}
}

func TestStoreFundingTransactionRollsBackOnDoubleSpend(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	fundingInput := model.UTXO{UtxoID: "old_0", TxID: "old", Vout: 0, Amount: 10000}
	if err := CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{fundingInput}); err != nil {
		t.Fatal(err)
	}
	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "other", TxHex: "00", Network: model.MAIN}, []model.Outpoint{{TxID: "old", Vout: 0}}); err != nil {
		t.Fatal(err)
	}

	err := StoreFundingTransaction(ctx, db, &model.Transaction{TxID: "fund", TxHex: "00", Network: model.MAIN}, []model.UTXO{fundingInput},
		[]model.QueueUTXO{{UTXO: model.UTXO{UtxoID: "fund_0", TxID: "fund", Vout: 0, Amount: 50}, Queue: "QUEUE_50"}},
		&model.UTXO{UtxoID: "fund_1", TxID: "fund", Vout: 1, Amount: 9900})
	var doubleSpendErr *DoubleSpendError
	if !errors.As(err, &doubleSpendErr) {
		t.Fatalf("expected a double spend, got %v", err)
	}

	if _, err := GetTransaction(ctx, db, "fund"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected the funding tx not to be stored, got %v", err)
	}
	unspent, err := GetAllUnspentFundingUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unspent) != 1 || unspent[0].UtxoID != "old_0" {
		t.Fatalf("expected the funding input to stay unspent and no change recorded, got %+v", unspent)
	}
	unpublished, err := GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 0 {
		t.Fatalf("expected no queue utxos recorded, got %+v", unpublished)
	}
}
