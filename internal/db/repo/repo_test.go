package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
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

	due, err := ClaimDueTransactions(ctx, db, 199, 250, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected nothing due before next_attempt_at, got %d", len(due))
	}
	due, err = ClaimDueTransactions(ctx, db, 200, 250, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due tx, got %d", len(due))
	}
	due, err = ClaimDueTransactions(ctx, db, 200, 250, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("expected a claimed tx not to be claimed again during its lease, got %d", len(due))
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
	due, err = ClaimDueTransactions(ctx, db, 1000, 1100, 10)
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
	if _, err := MarkFailed(ctx, db, "tx1", "not mined"); err != nil {
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
		if _, err := MarkFailed(ctx, db, "child1", "rejected"); err != nil {
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

	if _, err := MarkFailed(ctx, db, "tx1", "rejected"); err != nil {
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

func TestMarkFailedCascades(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	create := func(txID string, status model.TransactionStatus, inputs ...model.Outpoint) {
		t.Helper()
		if err := CreateTransaction(ctx, db, &model.Transaction{TxID: txID, TxHex: "00", Network: model.MAIN}, inputs); err != nil {
			t.Fatal(err)
		}
		if status == model.SYNCED {
			if err := MarkSynced(ctx, db, txID, "block", 1); err != nil {
				t.Fatal(err)
			}
		}
	}

	create("parent", model.PENDING, model.Outpoint{TxID: "coin", Vout: 0})
	create("child", model.PENDING, model.Outpoint{TxID: "parent", Vout: 0})
	create("grandchild", model.PENDING, model.Outpoint{TxID: "child", Vout: 0})
	create("mined", model.SYNCED, model.Outpoint{TxID: "parent", Vout: 1})
	create("unrelated", model.PENDING, model.Outpoint{TxID: "other", Vout: 0})

	if err := CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{{UtxoID: "parent_2", TxID: "parent", Vout: 2, Amount: 900}}); err != nil {
		t.Fatal(err)
	}

	cascaded, err := MarkFailed(ctx, db, "parent", "rejected")
	if err != nil {
		t.Fatal(err)
	}
	if len(cascaded) != 2 {
		t.Fatalf("expected child and grandchild to cascade, got %v", cascaded)
	}

	for txID, expected := range map[string]model.TransactionStatus{
		"parent": model.FAILED, "child": model.FAILED, "grandchild": model.FAILED, "mined": model.SYNCED, "unrelated": model.PENDING,
	} {
		got, err := GetTransaction(ctx, db, txID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != expected {
			t.Fatalf("expected %s to be %s, got %s", txID, expected, got.Status)
		}
	}

	unspent, err := GetAllUnspentFundingUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unspent) != 0 {
		t.Fatalf("expected the change of the failed tx to be unusable, got %+v", unspent)
	}
}

func storeTestFunding(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	fundingInput := model.UTXO{UtxoID: "old_0", TxID: "old", Vout: 0, Amount: 10000}
	if err := CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{fundingInput}); err != nil {
		t.Fatal(err)
	}
	err := StoreFundingTransaction(ctx, db, &model.Transaction{TxID: "fund", TxHex: "00", Network: model.MAIN}, []model.UTXO{fundingInput},
		[]model.QueueUTXO{{UTXO: model.UTXO{UtxoID: "fund_0", TxID: "fund", Vout: 0, Amount: 50}, Queue: "QUEUE_50"}},
		&model.UTXO{UtxoID: "fund_1", TxID: "fund", Vout: 1, Amount: 9900})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkQueueUTXOPublished(ctx, db, "fund_0"); err != nil {
		t.Fatal(err)
	}
}

func TestFailedFundingIsNotPublishedAndRecoversItsInput(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)
	storeTestFunding(t, db)

	if _, err := db.Exec(`UPDATE queue_utxos SET published = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkFailed(ctx, db, "fund", "rejected"); err != nil {
		t.Fatal(err)
	}

	unpublished, err := GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 0 {
		t.Fatalf("expected outputs of a failed funding tx never to be published, got %+v", unpublished)
	}
	if failed, err := IsTransactionFailed(ctx, db, "fund"); err != nil || !failed {
		t.Fatalf("expected fund to be failed, got %v %v", failed, err)
	}

	candidates, err := GetRecoveryCandidates(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Kind != RecoveryFunding || candidates[0].Outpoint.TxID != "old" || candidates[0].FailedTxID != "fund" {
		t.Fatalf("expected only the funding input as a candidate, got %+v", candidates)
	}

	recovered, err := RecoverUtxo(ctx, db, candidates[0])
	if err != nil || !recovered {
		t.Fatalf("expected recovery, got %v %v", recovered, err)
	}
	unspent, err := GetAllUnspentFundingUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unspent) != 1 || unspent[0].UtxoID != "old_0" {
		t.Fatalf("expected the funding input to be spendable again, got %+v", unspent)
	}
}

func TestRecoverFeeUtxoFromFailedClientTx(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)
	storeTestFunding(t, db)

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "client", TxHex: "00", Network: model.MAIN}, []model.Outpoint{{TxID: "fund", Vout: 0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkFailed(ctx, db, "client", "expired"); err != nil {
		t.Fatal(err)
	}

	candidates, err := GetRecoveryCandidates(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Kind != RecoveryFee || candidates[0].Outpoint.TxID != "fund" || candidates[0].FailedTxID != "client" {
		t.Fatalf("expected the fee utxo as a candidate, got %+v", candidates)
	}

	recovered, err := RecoverUtxo(ctx, db, candidates[0])
	if err != nil || !recovered {
		t.Fatalf("expected recovery, got %v %v", recovered, err)
	}
	recovered, err = RecoverUtxo(ctx, db, candidates[0])
	if err != nil || recovered {
		t.Fatalf("expected a second recovery to do nothing, got %v %v", recovered, err)
	}

	unpublished, err := GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 1 || unpublished[0].UtxoID != "fund_0" {
		t.Fatalf("expected the fee utxo to be republished, got %+v", unpublished)
	}
	if candidates, _ := GetRecoveryCandidates(ctx, db, 10); len(candidates) != 0 {
		t.Fatalf("expected no candidates after recovery, got %+v", candidates)
	}
}

func TestChainSpentIsNotACandidate(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)
	storeTestFunding(t, db)

	if err := CreateTransaction(ctx, db, &model.Transaction{TxID: "client", TxHex: "00", Network: model.MAIN}, []model.Outpoint{{TxID: "fund", Vout: 0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkFailed(ctx, db, "client", "expired"); err != nil {
		t.Fatal(err)
	}
	candidates, err := GetRecoveryCandidates(ctx, db, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %+v %v", candidates, err)
	}
	if err := MarkChainSpent(ctx, db, candidates[0]); err != nil {
		t.Fatal(err)
	}
	if candidates, _ := GetRecoveryCandidates(ctx, db, 10); len(candidates) != 0 {
		t.Fatalf("expected a chain-spent utxo not to be a candidate, got %+v", candidates)
	}
}

func TestReleaseClaims(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	for _, txID := range []string{"a", "b"} {
		if err := CreateTransaction(ctx, db, &model.Transaction{TxID: txID, TxHex: "00", Network: model.MAIN, NextAttemptAt: 100}, nil); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := ClaimDueTransactions(ctx, db, 100, 700, 10)
	if err != nil || len(claimed) != 2 || claimed[0].TxID != "a" || claimed[1].TxID != "b" {
		t.Fatalf("expected both claimed in insert order, got %+v %v", claimed, err)
	}

	if err := ReleaseClaims(ctx, db, []string{"b"}, 150); err != nil {
		t.Fatal(err)
	}
	due, err := ClaimDueTransactions(ctx, db, 150, 700, 10)
	if err != nil || len(due) != 1 || due[0].TxID != "b" {
		t.Fatalf("expected only the released tx to be claimable, got %+v %v", due, err)
	}
}

func TestConcurrentClaimsDoNotOverlap(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)
	db.SetMaxOpenConns(20)

	for i := 0; i < 40; i++ {
		if err := CreateTransaction(ctx, db, &model.Transaction{TxID: fmt.Sprint("tx", i), TxHex: "00", Network: model.MAIN, NextAttemptAt: 100}, nil); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[string]int{}
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := ClaimDueTransactions(ctx, db, 100, 700, 10)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, tx := range claimed {
				seen[tx.TxID]++
			}
		}()
	}
	wg.Wait()

	if len(seen) != 40 {
		t.Fatalf("expected all 40 txs claimed, got %d", len(seen))
	}
	for txID, count := range seen {
		if count != 1 {
			t.Fatalf("expected %s claimed once, got %d", txID, count)
		}
	}
}
