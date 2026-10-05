package services

import (
	"context"
	"fmt"
	"time"

	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/telemetry"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const (
	DEFAULT_MAX_ATTEMPTS         = 5
	DEFAULT_MAX_AGE              = 24 * time.Hour
	DEFAULT_REBROADCAST_INTERVAL = 10 * time.Minute
	DEFAULT_SYNC_POLL_INTERVAL   = 10 * time.Second
	DEFAULT_SYNC_BATCH_SIZE      = 50

	ARC_STATUS_MINED = "MINED"

	SYNC_CLAIM_LEASE = 10 * time.Minute
)

type SyncConfig struct {
	MaxAttempts         int
	MaxAge              time.Duration
	RebroadcastInterval time.Duration
	PollInterval        time.Duration
	BatchSize           int
}

func LoadSyncConfig() SyncConfig {
	cfg := SyncConfig{
		MaxAttempts:         viper.GetInt("sync.max_attempts"),
		MaxAge:              viper.GetDuration("sync.max_age"),
		RebroadcastInterval: viper.GetDuration("sync.rebroadcast_interval"),
		PollInterval:        viper.GetDuration("sync.poll_interval"),
		BatchSize:           viper.GetInt("sync.batch_size"),
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DEFAULT_MAX_ATTEMPTS
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DEFAULT_MAX_AGE
	}
	if cfg.RebroadcastInterval <= 0 {
		cfg.RebroadcastInterval = DEFAULT_REBROADCAST_INTERVAL
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DEFAULT_SYNC_POLL_INTERVAL
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DEFAULT_SYNC_BATCH_SIZE
	}
	return cfg
}

type syncAction int

const (
	actionBroadcast syncAction = iota
	actionMarkSynced
	actionMarkFailed
	actionMarkExpired
)

func decide(tx *model.Transaction, mined bool, expired bool, maxAttempts int) syncAction {
	if tx.Attempts > 0 && mined {
		return actionMarkSynced
	}
	if expired {
		return actionMarkExpired
	}
	if tx.Attempts >= maxAttempts {
		return actionMarkFailed
	}
	return actionBroadcast
}

func (r *RelayService) attemptBroadcast(ctx context.Context, tx *model.Transaction) (arcDown bool, err error) {
	ctx, span := telemetry.Start(ctx, "arc_broadcast", attribute.String("txid", tx.TxID), attribute.Int("attempt", tx.Attempts+1))
	defer func() {
		span.SetAttributes(attribute.Bool("arc_unreachable", arcDown))
		telemetry.End(span, err)
	}()

	broadcastctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	_, broadcastErr := r.broadcaster.Arc.BroadcastTx(broadcastctx, tx.TxHex, nil)
	cancel()

	now := time.Now()
	nextAttemptAt := now.Add(r.syncConfig.RebroadcastInterval).Unix()

	dbctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if broadcaster.IsUnreachable(broadcastErr) {
		telemetry.Log(ctx).Warn("arc unreachable, broadcast not counted as an attempt", zap.String("txid", tx.TxID), zap.Error(broadcastErr))
		return true, repo.RecordUnreachable(dbctx, r.db, tx.TxID, broadcastErr.Error(), now.Unix())
	}

	if broadcastErr != nil {
		telemetry.Log(ctx).Warn("broadcast rejected", zap.String("txid", tx.TxID), zap.Int("attempt", tx.Attempts+1), zap.Error(broadcastErr))
		return false, repo.RecordBroadcastError(dbctx, r.db, tx.TxID, broadcastErr.Error(), nextAttemptAt)
	}

	return false, repo.MarkBroadcasted(dbctx, r.db, tx.TxID, now.Unix(), nextAttemptAt)
}

func (r *RelayService) markFailed(ctx context.Context, txID string, reason string, errMsg string) error {
	telemetry.Log(ctx).Error("tx failed", zap.String("txid", txID), zap.String("reason", errMsg))
	cascaded, err := repo.MarkFailed(ctx, r.db, txID, errMsg)
	if err != nil {
		return err
	}
	telemetry.Count(ctx, telemetry.Metrics.TxSettled, attribute.String("status", "failed"), attribute.String("reason", reason))
	telemetry.CountN(ctx, telemetry.Metrics.TxSettled, int64(len(cascaded)), attribute.String("status", "failed"), attribute.String("reason", "parent_failed"))
	if len(cascaded) > 0 {
		telemetry.Log(ctx).Error("failed txs chained off a failed tx", zap.String("txid", txID), zap.Strings("cascaded", cascaded))
	}
	return nil
}

func (r *RelayService) StartSyncer(ctx context.Context) {
	ticker := time.NewTicker(r.syncConfig.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			r.syncDue(ctx)
		}
	}
}

func (r *RelayService) syncDue(ctx context.Context) {
	now := time.Now()
	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	transactions, err := repo.ClaimDueTransactions(dbctx, r.db, now.Unix(), now.Add(SYNC_CLAIM_LEASE).Unix(), r.syncConfig.BatchSize)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to claim due transactions", zap.Error(err))
		return
	}

	for i := range transactions {
		if ctx.Err() != nil {
			r.releaseClaims(transactions[i:])
			return
		}
		arcDown, err := r.syncTransaction(ctx, &transactions[i])
		if err != nil {
			telemetry.Log(ctx).Error("failed to sync tx", zap.String("txid", transactions[i].TxID), zap.Error(err))
		}
		if arcDown {
			telemetry.Log(ctx).Warn("arc unreachable, pausing sync until the next poll")
			r.releaseClaims(transactions[i:])
			return
		}
	}
}

func (r *RelayService) releaseClaims(transactions []model.Transaction) {
	txIDs := make([]string, 0, len(transactions))
	for _, tx := range transactions {
		txIDs = append(txIDs, tx.TxID)
	}

	dbctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := repo.ReleaseClaims(dbctx, r.db, txIDs, time.Now().Unix()); err != nil {
		zap.L().Warn("failed to release claimed transactions", zap.Duration("retry_after", SYNC_CLAIM_LEASE), zap.Error(err))
	}
}

func (r *RelayService) isExpired(tx *model.Transaction, now time.Time) bool {
	return now.Sub(time.Unix(tx.CreatedAt, 0)) >= r.syncConfig.MaxAge
}

func (r *RelayService) syncTransaction(ctx context.Context, tx *model.Transaction) (arcDown bool, err error) {
	ctx, span := telemetry.Start(ctx, "sync_tx", attribute.String("txid", tx.TxID), attribute.Int("attempts", tx.Attempts))
	defer func() { telemetry.End(span, err) }()

	expired := r.isExpired(tx, time.Now())
	mined := false
	var blockHash string
	var blockHeight uint64

	if tx.Attempts > 0 {
		statusctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		status, err := r.broadcaster.Arc.GetTxStatus(statusctx, tx.TxID)
		cancel()
		switch {
		case err == nil:
			if status.TxStatus == ARC_STATUS_MINED {
				mined = true
				blockHash = status.BlockHash
				blockHeight = status.BlockHeight
			}
		case broadcaster.IsUnreachable(err) && !expired:
			return true, nil
		default:
			telemetry.Log(ctx).Warn("failed to get tx status", zap.String("txid", tx.TxID), zap.Error(err))
		}
	}

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch decide(tx, mined, expired, r.syncConfig.MaxAttempts) {
	case actionMarkSynced:
		telemetry.Log(ctx).Info("tx mined", zap.String("txid", tx.TxID), zap.Uint64("block_height", blockHeight))
		if err := repo.MarkSynced(dbctx, r.db, tx.TxID, blockHash, blockHeight); err != nil {
			return false, err
		}
		telemetry.Count(ctx, telemetry.Metrics.TxSettled, attribute.String("status", "synced"), attribute.String("reason", "mined"))
		if r := telemetry.Metrics.TxTimeToMined; r != nil {
			r.Record(ctx, time.Since(time.Unix(tx.CreatedAt, 0)).Seconds())
		}
		return false, nil

	case actionMarkExpired:
		errMsg := fmt.Sprintf("expired after %s without being mined", r.syncConfig.MaxAge)
		if tx.LastError != "" {
			errMsg = fmt.Sprintf("%s, last error: %s", errMsg, tx.LastError)
		}
		return false, r.markFailed(dbctx, tx.TxID, "expired", errMsg)

	case actionMarkFailed:
		errMsg := fmt.Sprintf("not mined after %d broadcast attempts", tx.Attempts)
		if tx.LastError != "" {
			errMsg = fmt.Sprintf("%s, last error: %s", errMsg, tx.LastError)
		}
		return false, r.markFailed(dbctx, tx.TxID, "max_attempts", errMsg)

	default:
		return r.attemptBroadcast(ctx, tx)
	}
}
