package services

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"
)

const (
	DEFAULT_FEE_SATOSHIS        = 100
	DEFAULT_FEE_BYTES           = 1000
	DEFAULT_FEE_POLICY_INTERVAL = 30 * time.Minute
)

type FeeRate struct {
	Satoshis uint64
	Bytes    uint64
}

var feeRate atomic.Pointer[FeeRate]

func currentFeeRate() FeeRate {
	if rate := feeRate.Load(); rate != nil {
		return *rate
	}
	return FeeRate{Satoshis: DEFAULT_FEE_SATOSHIS, Bytes: DEFAULT_FEE_BYTES}
}

func setFeeRate(rate FeeRate) {
	feeRate.Store(&rate)
}

func feeForSize(size int) uint64 {
	rate := currentFeeRate()
	return (uint64(size)*rate.Satoshis + rate.Bytes - 1) / rate.Bytes
}

func minChange() uint64 {
	return feeForSize(INPUT_SIZE)
}

func feePolicyInterval() time.Duration {
	interval := viper.GetDuration("fee_policy_interval")
	if interval <= 0 {
		interval = DEFAULT_FEE_POLICY_INTERVAL
	}
	return interval
}

func (r *RelayService) refreshFeeRate(ctx context.Context) error {
	policyctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	policy, err := r.broadcaster.Arc.GetPolicy(policyctx)
	if err != nil {
		return err
	}

	miningFee := policy.Policy.MiningFee
	if miningFee.Bytes == 0 {
		return errors.New("arc policy has a mining fee with zero bytes")
	}

	rate := FeeRate{Satoshis: miningFee.Satoshis, Bytes: miningFee.Bytes}
	if rate != currentFeeRate() {
		log.Printf("fee rate set to %d sats per %d bytes from arc policy\n", rate.Satoshis, rate.Bytes)
	}
	setFeeRate(rate)
	return nil
}

func (r *RelayService) StartFeePolicy(ctx context.Context) {
	ticker := time.NewTicker(feePolicyInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.refreshFeeRate(ctx); err != nil {
				rate := currentFeeRate()
				log.Printf("warning: failed to refresh fee rate, keeping %d sats per %d bytes: %v\n", rate.Satoshis, rate.Bytes, err)
			}
		}
	}
}
