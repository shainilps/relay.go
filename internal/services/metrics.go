package services

import (
	"context"
	"time"

	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

func (r *RelayService) RegisterMetrics() error {
	meter := telemetry.Meter()

	fundingBalance, err := meter.Int64ObservableGauge("relay.funding.balance", metric.WithDescription("Unspent funding sats"), metric.WithUnit("sat"))
	if err != nil {
		return err
	}
	fundingUtxos, err := meter.Int64ObservableGauge("relay.funding.utxos", metric.WithDescription("Unspent funding utxos"))
	if err != nil {
		return err
	}
	txCount, err := meter.Int64ObservableGauge("relay.tx.count", metric.WithDescription("Stored txs by status"))
	if err != nil {
		return err
	}
	unpublished, err := meter.Int64ObservableGauge("relay.queue_utxo.unpublished", metric.WithDescription("Fee utxos created but not yet published to their queue"))
	if err != nil {
		return err
	}
	owed, err := meter.Int64ObservableGauge("relay.funding.owed", metric.WithDescription("Fee utxos owed to each queue by this instance"))
	if err != nil {
		return err
	}
	parked, err := meter.Int64ObservableGauge("relay.fee_utxo.parked", metric.WithDescription("Fee utxo copies parked while another request holds them"))
	if err != nil {
		return err
	}
	feeRate, err := meter.Float64ObservableGauge("relay.fee.rate", metric.WithDescription("Mining fee rate in use"), metric.WithUnit("sat/kB"))
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		dbctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		if balance, count, err := repo.GetFundingBalance(dbctx, r.db); err != nil {
			zap.L().Warn("failed to read funding balance for metrics", zap.Error(err))
		} else {
			observer.ObserveInt64(fundingBalance, balance)
			observer.ObserveInt64(fundingUtxos, count)
		}

		if counts, err := repo.CountTransactionsByStatus(dbctx, r.db); err != nil {
			zap.L().Warn("failed to count txs for metrics", zap.Error(err))
		} else {
			for status, count := range counts {
				observer.ObserveInt64(txCount, count, metric.WithAttributes(attribute.String("status", string(status))))
			}
		}

		if count, err := repo.CountUnpublishedQueueUTXOs(dbctx, r.db); err != nil {
			zap.L().Warn("failed to count unpublished queue utxos for metrics", zap.Error(err))
		} else {
			observer.ObserveInt64(unpublished, count)
		}

		r.deficitMu.Lock()
		for queuename, count := range r.deficit {
			observer.ObserveInt64(owed, int64(count), metric.WithAttributes(attribute.String("queue", string(queuename))))
		}
		r.deficitMu.Unlock()

		r.parkedMu.Lock()
		observer.ObserveInt64(parked, int64(len(r.parked)))
		r.parkedMu.Unlock()

		rate := currentFeeRate()
		observer.ObserveFloat64(feeRate, float64(rate.Satoshis)*1000/float64(rate.Bytes))
		return nil
	}, fundingBalance, fundingUtxos, txCount, unpublished, owed, parked, feeRate)
	return err
}
