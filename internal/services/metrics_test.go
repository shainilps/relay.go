package services

import (
	"context"
	"testing"

	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	found := map[string]metricdata.Aggregation{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			found[m.Name] = m.Data
		}
	}
	return found
}

func TestMetricsAreRecorded(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	if err := repo.CreateFundingUTXOsIfNotExists(ctx, db, []model.UTXO{{UtxoID: "deposit_0", TxID: "deposit", Vout: 0, Amount: 5000}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTransaction(ctx, db, &model.Transaction{TxID: "pending"}, nil); err != nil {
		t.Fatal(err)
	}

	r := NewRelayService(db, nil, &fakeQueue{}, nil)
	if err := r.RegisterMetrics(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Broadcast(ctx, "not hex"); err == nil {
		t.Fatal("expected invalid hex to be rejected")
	}

	found := collectMetrics(t, reader)

	submitted, ok := found["relay.tx.submitted"].(metricdata.Sum[int64])
	if !ok || len(submitted.DataPoints) != 1 || submitted.DataPoints[0].Value != 1 {
		t.Fatalf("expected one submission counted, got %+v", found["relay.tx.submitted"])
	}
	if result, _ := submitted.DataPoints[0].Attributes.Value("result"); result.AsString() != "invalid" {
		t.Fatalf("expected the submission to be counted as invalid, got %v", result.AsString())
	}

	balance, ok := found["relay.funding.balance"].(metricdata.Gauge[int64])
	if !ok || len(balance.DataPoints) != 1 || balance.DataPoints[0].Value != 5000 {
		t.Fatalf("expected the funding balance gauge to be 5000, got %+v", found["relay.funding.balance"])
	}

	txCount, ok := found["relay.tx.count"].(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("expected a tx count gauge, got %+v", found["relay.tx.count"])
	}
	pending := int64(-1)
	for _, point := range txCount.DataPoints {
		if status, _ := point.Attributes.Value("status"); status.AsString() == string(model.PENDING) {
			pending = point.Value
		}
	}
	if pending != 1 {
		t.Fatalf("expected one pending tx, got %d", pending)
	}

	if rate, ok := found["relay.fee.rate"].(metricdata.Gauge[float64]); !ok || rate.DataPoints[0].Value != 100 {
		t.Fatalf("expected the default fee rate of 100 sat/kB, got %+v", found["relay.fee.rate"])
	}
}
