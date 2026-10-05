package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
)

func TestRecoverUtxos(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	fundingInput := model.UTXO{UtxoID: "old_0", TxID: "old", Vout: 0, Amount: 10000}
	if err := repo.CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{fundingInput}); err != nil {
		t.Fatal(err)
	}
	queueUtxos := []model.QueueUTXO{
		{UTXO: model.UTXO{UtxoID: "fund_0", TxID: "fund", Vout: 0, Amount: 50}, Queue: "QUEUE_50"},
		{UTXO: model.UTXO{UtxoID: "fund_1", TxID: "fund", Vout: 1, Amount: 50}, Queue: "QUEUE_50"},
	}
	if err := repo.StoreFundingTransaction(ctx, db, &model.Transaction{TxID: "fund", TxHex: "00"}, []model.UTXO{fundingInput}, queueUtxos, nil); err != nil {
		t.Fatal(err)
	}
	for _, utxo := range queueUtxos {
		if err := repo.MarkQueueUTXOPublished(ctx, db, utxo.UtxoID); err != nil {
			t.Fatal(err)
		}
	}

	for i, client := range []string{"client_a", "client_b"} {
		if err := repo.CreateTransaction(ctx, db, &model.Transaction{TxID: client, TxHex: "00"}, []model.Outpoint{{TxID: "fund", Vout: uint32(i)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.MarkFailed(ctx, db, client, "expired"); err != nil {
			t.Fatal(err)
		}
	}

	explorer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main/tx/fund/0/spent":
			w.WriteHeader(http.StatusNotFound)
		case "/main/tx/fund/1/spent":
			w.Write([]byte(`{"txid":"elsewhere","vin":0,"status":"confirmed"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer explorer.Close()

	r := NewRelayService(db, &broadcaster.Broadcaster{Explorer: broadcaster.NewWOCExplorer(explorer.URL, model.MAIN, "")}, &fakeQueue{}, nil)
	r.recoverUtxos(ctx)

	unpublished, err := repo.GetUnpublishedQueueUTXOs(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(unpublished) != 1 || unpublished[0].UtxoID != "fund_0" {
		t.Fatalf("expected only the unspent fee utxo to be republished, got %+v", unpublished)
	}
	if candidates, _ := repo.GetRecoveryCandidates(ctx, db, 10); len(candidates) != 0 {
		t.Fatalf("expected nothing left to recover, got %+v", candidates)
	}
	select {
	case <-r.fundingChan:
	default:
		t.Fatal("expected the funder to be woken to publish the recovered utxo")
	}
}
