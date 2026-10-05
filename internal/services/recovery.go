package services

import (
	"context"
	"time"

	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/telemetry"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	DEFAULT_RECOVERY_INTERVAL = 10 * time.Minute
	RECOVERY_BATCH_SIZE       = 50
	EXPLORER_CALL_GAP         = 400 * time.Millisecond
)

func recoveryInterval() time.Duration {
	interval := viper.GetDuration("recovery_interval")
	if interval <= 0 {
		interval = DEFAULT_RECOVERY_INTERVAL
	}
	return interval
}

func (r *RelayService) StartRecovery(ctx context.Context) {
	ticker := time.NewTicker(recoveryInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.recoverUtxos(ctx)
		}
	}
}

func (r *RelayService) recoverUtxos(ctx context.Context) {
	ctx, span := telemetry.Start(ctx, "recover_utxos")
	defer span.End()

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	candidates, err := repo.GetRecoveryCandidates(dbctx, r.db, RECOVERY_BATCH_SIZE)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to load recovery candidates", zap.Error(err))
		return
	}

	recoveredFee := false
	for i, candidate := range candidates {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(EXPLORER_CALL_GAP):
			}
		}

		checkctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		status, spentBy, err := r.broadcaster.Explorer.GetOutputSpent(checkctx, candidate.Outpoint.TxID, candidate.Outpoint.Vout)
		cancel()
		if err != nil {
			telemetry.Count(ctx, telemetry.Metrics.UtxoRecovery, attribute.String("kind", candidate.Kind), attribute.String("result", "check_failed"))
			telemetry.Log(ctx).Warn("failed to check utxo on chain", zap.String("kind", candidate.Kind), zap.String("txid", candidate.Outpoint.TxID), zap.Uint32("vout", candidate.Outpoint.Vout), zap.Error(err))
			continue
		}

		dbctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		switch status {
		case broadcaster.OutputUnspent:
			recovered, err := repo.RecoverUtxo(dbctx, r.db, candidate)
			if err != nil {
				telemetry.Log(ctx).Error("failed to recover utxo", zap.String("kind", candidate.Kind), zap.String("txid", candidate.Outpoint.TxID), zap.Uint32("vout", candidate.Outpoint.Vout), zap.Error(err))
			} else if recovered {
				telemetry.Count(ctx, telemetry.Metrics.UtxoRecovery, attribute.String("kind", candidate.Kind), attribute.String("result", "recovered"))
				telemetry.Log(ctx).Info("recovered utxo from failed tx", zap.String("kind", candidate.Kind), zap.String("txid", candidate.Outpoint.TxID), zap.Uint32("vout", candidate.Outpoint.Vout), zap.String("failed_txid", candidate.FailedTxID))
				if candidate.Kind == repo.RecoveryFee {
					recoveredFee = true
				}
			}

		case broadcaster.OutputSpent:
			if spentBy == candidate.FailedTxID {
				telemetry.Log(ctx).Warn("tx is marked FAILED but its input is spent by it on chain", zap.String("failed_txid", candidate.FailedTxID), zap.String("txid", candidate.Outpoint.TxID), zap.Uint32("vout", candidate.Outpoint.Vout))
			}
			telemetry.Count(ctx, telemetry.Metrics.UtxoRecovery, attribute.String("kind", candidate.Kind), attribute.String("result", "chain_spent"))
			if err := repo.MarkChainSpent(dbctx, r.db, candidate); err != nil {
				telemetry.Log(ctx).Error("failed to mark utxo as spent on chain", zap.String("kind", candidate.Kind), zap.String("txid", candidate.Outpoint.TxID), zap.Uint32("vout", candidate.Outpoint.Vout), zap.Error(err))
			}
		}
		cancel()
	}

	if recoveredFee {
		r.signalFunding()
	}
}
