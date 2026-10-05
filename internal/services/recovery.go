package services

import (
	"context"
	"log"
	"time"

	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/spf13/viper"
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
	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	candidates, err := repo.GetRecoveryCandidates(dbctx, r.db, RECOVERY_BATCH_SIZE)
	cancel()
	if err != nil {
		log.Printf("critical: failed to load recovery candidates: %v\n", err)
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
			log.Printf("warning: failed to check %s utxo %s:%d on chain: %v\n", candidate.Kind, candidate.Outpoint.TxID, candidate.Outpoint.Vout, err)
			continue
		}

		dbctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		switch status {
		case broadcaster.OutputUnspent:
			recovered, err := repo.RecoverUtxo(dbctx, r.db, candidate)
			if err != nil {
				log.Printf("critical: failed to recover %s utxo %s:%d: %v\n", candidate.Kind, candidate.Outpoint.TxID, candidate.Outpoint.Vout, err)
			} else if recovered {
				log.Printf("recovered %s utxo %s:%d from failed tx %s\n", candidate.Kind, candidate.Outpoint.TxID, candidate.Outpoint.Vout, candidate.FailedTxID)
				if candidate.Kind == repo.RecoveryFee {
					recoveredFee = true
				}
			}

		case broadcaster.OutputSpent:
			if spentBy == candidate.FailedTxID {
				log.Printf("warning: tx %s is marked FAILED but spends %s:%d on chain\n", candidate.FailedTxID, candidate.Outpoint.TxID, candidate.Outpoint.Vout)
			}
			if err := repo.MarkChainSpent(dbctx, r.db, candidate); err != nil {
				log.Printf("critical: failed to mark %s utxo %s:%d as spent on chain: %v\n", candidate.Kind, candidate.Outpoint.TxID, candidate.Outpoint.Vout, err)
			}
		}
		cancel()
	}

	if recoveredFee {
		r.signalFunding()
	}
}
