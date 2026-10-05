package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/shainilps/relay/internal/reservation"
	"github.com/spf13/viper"

	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/shainilps/relay/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

const DEFAULT_FUND_AMOUNT = 1
const FUNDING_RETRY_INTERVAL = 30 * time.Second
const DEFAULT_FUNDING_SCAN_INTERVAL = 5 * time.Minute
const DEFAULT_MAX_SPONSOR_SATS = 20000
const PARKED_CHECK_INTERVAL = time.Second
const FUNDING_LOCK = "funding"
const FUNDING_LOCK_TTL = 5 * time.Minute
const FUNDING_LOCK_RETRY_INTERVAL = 5 * time.Second
const INPUT_SIZE = 149 // this is can be 149 also because DER singature can be 32/33
const OUTPUT_SIZE = 34

func (r *RelayService) StartEngine(ctx context.Context) {
	if err := r.refreshFeeRate(ctx); err != nil {
		rate := currentFeeRate()
		telemetry.Log(ctx).Warn("failed to load fee rate from arc policy, using the default", zap.Uint64("satoshis", rate.Satoshis), zap.Uint64("bytes", rate.Bytes), zap.Error(err))
	}
	go r.StartFeePolicy(ctx)
	go r.watchParked(ctx)
	go r.StartRecovery(ctx)
	r.syncFundingUtxos(ctx)
	go r.scanFundingUtxos(ctx)
	r.ingestUtxos(ctx)
}

func fundingScanInterval() time.Duration {
	interval := viper.GetDuration("funding_scan_interval")
	if interval <= 0 {
		interval = DEFAULT_FUNDING_SCAN_INTERVAL
	}
	return interval
}

func (r *RelayService) scanFundingUtxos(ctx context.Context) {
	ticker := time.NewTicker(fundingScanInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.syncFundingUtxos(ctx)
			r.signalFunding()
		}
	}
}

func (r *RelayService) syncFundingUtxos(ctx context.Context) {
	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		telemetry.Log(ctx).Error("failed to fetch funding address from key manager", zap.Error(err))
		return
	}

	reqctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	utxosets, err := r.broadcaster.Explorer.GetUtxosForAddress(reqctx, address.AddressString)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Warn("failed to fetch funding utxos", zap.String("address", address.AddressString), zap.Error(err))
		return
	}

	fundingUtxo := make([]model.UTXO, 0, len(utxosets.Result))
	for _, utxo := range utxosets.Result {
		if !utxo.IsSpentInMempoolTx {
			fundingUtxo = append(fundingUtxo, model.UTXO{
				UtxoID: fmt.Sprintf("%s_%d", utxo.TxHash, utxo.TxPos),
				Amount: utxo.Value,
				TxID:   utxo.TxHash,
				Vout:   utxo.TxPos,
			})
		}
	}
	if len(fundingUtxo) == 0 {
		return
	}

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = repo.CreateFundingUTXOsIfNotExists(dbctx, r.db, fundingUtxo)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Warn("failed to insert funding utxos", zap.Error(err))
	}
}

func fundTarget() int {
	fundAmount := viper.GetInt("fund_amount")
	if fundAmount <= 0 {
		fundAmount = DEFAULT_FUND_AMOUNT
	}
	return fundAmount
}

func (r *RelayService) recordDeficit(counts map[rabbitmq.QueueName]int) {
	r.deficitMu.Lock()
	defer r.deficitMu.Unlock()
	for queuename, count := range counts {
		r.deficit[queuename] += count
	}
}

func (r *RelayService) takeDeficit() map[rabbitmq.QueueName]int {
	r.deficitMu.Lock()
	defer r.deficitMu.Unlock()
	deficit := r.deficit
	r.deficit = make(map[rabbitmq.QueueName]int)
	return deficit
}

func (r *RelayService) signalFunding() {
	select {
	case r.fundingChan <- struct{}{}:
	default:
	}
}

func (r *RelayService) ingestUtxos(ctx context.Context) {

	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		zap.L().Fatal("failed to fetch the funding address from the key manager", zap.Error(err))
	}

	fundingLockingScript, err := p2pkh.Lock(address)
	if err != nil {
		zap.L().Fatal("failed to build the funding locking script", zap.Error(err))
	}

	feeAddress, err := keymanager.KeyManager.GetFeeAddress()
	if err != nil {
		zap.L().Fatal("failed to fetch the fee address from the key manager", zap.Error(err))
	}

	feeLockingScript, err := p2pkh.Lock(feeAddress)
	if err != nil {
		zap.L().Fatal("failed to build the fee locking script", zap.Error(err))
	}

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	unpublished, err := repo.GetUnpublishedQueueUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		zap.L().Fatal("failed to load unpublished queue utxos", zap.Error(err))
	}
	pending := make(map[rabbitmq.QueueName]int)
	for _, utxo := range unpublished {
		pending[rabbitmq.QueueName(utxo.Queue)]++
	}

	target := fundTarget()
	startup := make(map[rabbitmq.QueueName]int)
	for queuename, queue := range r.mq.Queues() {
		if queue.Consumers > 0 {
			continue
		}
		if missing := target - queue.Messages - pending[queuename]; missing > 0 {
			startup[queuename] = missing
		}
	}
	telemetry.Log(ctx).Info("startup funding deficit", zap.Any("deficit", startup), zap.Any("unpublished", pending))
	r.recordDeficit(startup)
	r.signalFunding()

	var retry <-chan time.Time
	for {
		signal := r.fundingChan
		if retry != nil {
			signal = nil
		}

		select {
		case <-ctx.Done():
			return
		case <-signal:
		case <-retry:
			retry = nil
		}

		token, locked := r.acquireFundingLock(ctx)
		if !locked {
			telemetry.Count(ctx, telemetry.Metrics.FundingRounds, attribute.String("result", "lock_busy"))
			retry = time.After(FUNDING_LOCK_RETRY_INTERVAL)
			continue
		}

		deficit := r.takeDeficit()
		if len(deficit) > 0 {
			unfunded := r.fundQueues(ctx, deficit, fundingLockingScript, feeLockingScript)
			if len(unfunded) > 0 {
				r.recordDeficit(unfunded)
				retry = time.After(FUNDING_RETRY_INTERVAL)
				telemetry.Log(ctx).Warn("queues left unfunded, retrying", zap.Any("unfunded", unfunded), zap.Duration("retry_in", FUNDING_RETRY_INTERVAL))
			}
		}

		if !r.publishPending(ctx) && retry == nil {
			retry = time.After(FUNDING_RETRY_INTERVAL)
			telemetry.Log(ctx).Warn("queue utxos left unpublished, retrying", zap.Duration("retry_in", FUNDING_RETRY_INTERVAL))
		}

		r.releaseFundingLock(token)
	}
}

func (r *RelayService) acquireFundingLock(ctx context.Context) (string, bool) {
	if r.reservations == nil {
		return "", true
	}

	lockctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	token, ok, err := r.reservations.AcquireLock(lockctx, FUNDING_LOCK, FUNDING_LOCK_TTL)
	if err != nil {
		telemetry.Log(ctx).Warn("failed to take the funding lock, funding without it", zap.Error(err))
		return "", true
	}
	if !ok {
		telemetry.Log(ctx).Info("funding lock is held by another instance", zap.Duration("retry_in", FUNDING_LOCK_RETRY_INTERVAL))
	}
	return token, ok
}

func (r *RelayService) releaseFundingLock(token string) {
	if r.reservations == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := r.reservations.ReleaseLock(ctx, FUNDING_LOCK, token); err != nil {
		zap.L().Warn("failed to release the funding lock", zap.Duration("expires_in", FUNDING_LOCK_TTL), zap.Error(err))
	}
}

func varIntSize(n int) int {
	switch {
	case n < 0xfd:
		return 1
	case n <= 0xffff:
		return 3
	case n <= 0xffffffff:
		return 5
	default:
		return 9
	}
}

func p2pkhTxSize(inputCount int, outputCount int) int {
	return 4 + varIntSize(inputCount) + inputCount*INPUT_SIZE + varIntSize(outputCount) + outputCount*OUTPUT_SIZE + 4
}

func planChange(inputAmount uint64, outputAmount uint64, inputCount int, outputCount int) (uint64, uint64, bool) {
	fee := feeForSize(p2pkhTxSize(inputCount, outputCount))
	if inputAmount < outputAmount+fee {
		return 0, fee, false
	}

	feeWithChange := feeForSize(p2pkhTxSize(inputCount, outputCount+1))
	if inputAmount < outputAmount+feeWithChange+minChange() {
		return 0, inputAmount - outputAmount, true
	}

	return inputAmount - outputAmount - feeWithChange, feeWithChange, true
}

func (r *RelayService) fundQueues(ctx context.Context, deficit map[rabbitmq.QueueName]int, fundingLockingScript *script.Script, feeLockingScript *script.Script) (unfunded map[rabbitmq.QueueName]int) {
	ctx, span := telemetry.Start(ctx, "fund_queues")
	defer func() {
		result := "funded"
		if len(unfunded) > 0 {
			result = "failed"
		}
		span.SetAttributes(attribute.String("result", result))
		telemetry.Count(ctx, telemetry.Metrics.FundingRounds, attribute.String("result", result))
		span.End()
	}()

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	unxpentUtxos, err := repo.GetAllUnspentFundingUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to fetch funding utxos from db", zap.Error(err))
		return deficit
	}

	if len(unxpentUtxos) == 0 {
		telemetry.Log(ctx).Warn("out of funding utxos")
		return deficit
	}

	tx := transaction.NewTransaction()

	sgh := sighash.AllForkID
	unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetPrivateKey(), &sgh)
	if err != nil {
		telemetry.Log(ctx).Error("failed to build the funding unlocking script", zap.Error(err))
		return deficit
	}

	var inputAmount uint64
	for _, utxo := range unxpentUtxos {
		err = tx.AddInputFrom(utxo.TxID, utxo.Vout, hex.EncodeToString(fundingLockingScript.Bytes()), utxo.Amount, unlockingTemplate)
		if err != nil {
			telemetry.Log(ctx).Error("failed to add funding utxo to transaction", zap.String("utxo", utxo.UtxoID), zap.Error(err))
		}
		inputAmount += utxo.Amount
	}

	var outputAmount uint64
	outputQueues := make([]rabbitmq.QueueName, 0)
	for _, queuename := range rabbitmq.Queues {
		for range deficit[queuename] {
			tx.AddOutput(&transaction.TransactionOutput{
				Satoshis:      rabbitmq.QueueToValue[queuename],
				LockingScript: feeLockingScript,
			})
			outputAmount += rabbitmq.QueueToValue[queuename]
			outputQueues = append(outputQueues, queuename)
		}
	}
	fundCount := len(outputQueues)

	change, fee, ok := planChange(inputAmount, outputAmount, tx.InputCount(), tx.OutputCount())
	if !ok {
		telemetry.Log(ctx).Error("funding balance too low to fund queues", zap.Any("deficit", deficit), zap.Uint64("balance", inputAmount), zap.Uint64("needed", outputAmount+fee))
		return deficit
	}

	if change > 0 {
		tx.AddOutput(&transaction.TransactionOutput{
			Satoshis:      change,
			LockingScript: fundingLockingScript,
		})
	}

	err = tx.Sign()
	if err != nil {
		telemetry.Log(ctx).Error("failed to sign funding transaction", zap.Error(err))
		return deficit
	}

	if err := verifyScripts(tx, true); err != nil {
		telemetry.Log(ctx).Error("funding transaction failed validation", zap.Error(err))
		return deficit
	}

	extendedHex, err := tx.EFHex()
	if err != nil {
		telemetry.Log(ctx).Error("failed to encode funding transaction", zap.Any("deficit", deficit), zap.Error(err))
		return deficit
	}

	txID := tx.TxID().String()

	queueUtxos := make([]model.QueueUTXO, 0, fundCount)
	for i := range fundCount {
		queueUtxos = append(queueUtxos, model.QueueUTXO{
			UTXO: model.UTXO{
				UtxoID: fmt.Sprintf("%s_%d", txID, i),
				TxID:   txID,
				Vout:   uint32(i),
				Amount: tx.Outputs[i].Satoshis,
			},
			Queue: string(outputQueues[i]),
		})
	}

	var changeUtxo *model.UTXO
	if len(tx.Outputs) == fundCount+1 {
		changeUtxo = &model.UTXO{
			UtxoID: fmt.Sprintf("%s_%d", txID, fundCount),
			TxID:   txID,
			Vout:   uint32(fundCount),
			Amount: tx.Outputs[fundCount].Satoshis,
		}
	}

	fundingTx := &model.Transaction{
		TxID:          txID,
		TxHex:         extendedHex,
		NextAttemptAt: time.Now().Add(r.syncConfig.RebroadcastInterval).Unix(),
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = repo.StoreFundingTransaction(dbctx, r.db, fundingTx, unxpentUtxos, queueUtxos, changeUtxo)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to store funding transaction", zap.String("txid", txID), zap.Error(err))
		return deficit
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	stored, err := repo.GetTransaction(dbctx, r.db, txID)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to load stored funding transaction, leaving it to the syncer", zap.String("txid", txID), zap.Error(err))
		return nil
	}

	if _, err := r.attemptBroadcast(ctx, stored); err != nil {
		telemetry.Log(ctx).Error("failed to record broadcast of funding transaction", zap.String("txid", txID), zap.Error(err))
	}

	for queuename, count := range deficit {
		telemetry.CountN(ctx, telemetry.Metrics.FundingOutputs, int64(count), attribute.String("queue", string(queuename)))
	}
	span.SetAttributes(attribute.String("txid", txID))
	telemetry.Log(ctx).Info("funded queues", zap.Any("deficit", deficit), zap.String("txid", txID))
	return nil
}

func (r *RelayService) publishPending(ctx context.Context) bool {
	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pending, err := repo.GetUnpublishedQueueUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		telemetry.Log(ctx).Error("failed to load unpublished queue utxos", zap.Error(err))
		return false
	}

	for _, utxo := range pending {
		publishctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := r.mq.Publish(publishctx, rabbitmq.QueueName(utxo.Queue), &utxo.UTXO)
		cancel()
		if err != nil {
			telemetry.Log(ctx).Error("failed to publish utxo", zap.String("utxo", utxo.UtxoID), zap.String("queue", utxo.Queue), zap.Error(err))
			return false
		}

		dbctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = repo.MarkQueueUTXOPublished(dbctx, r.db, utxo.UtxoID)
		cancel()
		if err != nil {
			telemetry.Log(ctx).Error("failed to mark utxo as published", zap.String("utxo", utxo.UtxoID), zap.Error(err))
			return false
		}
	}

	return true
}

func maxSponsorSats() uint64 {
	limit := viper.GetUint64("max_sponsor_sats")
	if limit == 0 {
		limit = DEFAULT_MAX_SPONSOR_SATS
	}
	return limit
}

func sponsorAmount(tx *transaction.Transaction, limit uint64) (uint64, error) {
	inputAmount, err := tx.TotalInputSatoshis()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}

	needed := tx.TotalOutputSatoshis() + feeForSize(tx.Size())
	if inputAmount >= needed {
		return 0, nil
	}

	missing := needed - inputAmount
	if missing > limit {
		return 0, fmt.Errorf("%w: needs %d sats from the relay, the limit is %d", ErrInvalidTransaction, missing, limit)
	}
	return missing, nil
}

func (r *RelayService) AddUtxo(ctx context.Context, tx *transaction.Transaction) (taken []heldUtxo, err error) {
	ctx, span := telemetry.Start(ctx, "take_fee_utxos")
	defer func() {
		span.SetAttributes(attribute.Int("fee_utxos", len(taken)))
		telemetry.End(span, err)
	}()

	amount, err := sponsorAmount(tx, maxSponsorSats())
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Int64("sponsor_sats", int64(amount)))
	if amount == 0 {
		return nil, nil
	}

	address, err := keymanager.KeyManager.GetFeeAddress()
	if err != nil {
		return nil, err
	}
	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		return nil, err
	}
	lockingScriptStr := hex.EncodeToString(lockingScript.Bytes())

	sgh := sighash.All | sighash.AnyOneCanPay | sighash.ForkID
	unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetFeePrivateKey(), &sgh)
	if err != nil {
		return nil, err
	}

	queunames := CalcuateQueues(amount)

	held := make([]heldUtxo, 0, len(queunames))
	fail := func(err error) ([]heldUtxo, error) {
		r.returnHeld(held)
		return nil, err
	}

	for _, queuename := range queunames {
		taken, utxo, err := r.takeUtxo(ctx, queuename)
		if err != nil {
			return fail(err)
		}
		held = append(held, taken)

		err = tx.AddInputFrom(utxo.TxID, utxo.Vout, lockingScriptStr, utxo.Amount, unlockingTemplate)
		if err != nil {
			return fail(err)
		}
	}

	err = tx.Sign()
	if err != nil {
		return fail(err)
	}

	return held, nil
}

func (r *RelayService) takeUtxo(ctx context.Context, queuename rabbitmq.QueueName) (heldUtxo, model.UTXO, error) {
	timeout := time.After(10 * time.Second)

	for {
		select {

		case <-timeout:
			return heldUtxo{}, model.UTXO{}, fmt.Errorf("%w: timed out waiting utxo from queue %v", ErrOutOfFee, queuename)

		case message := <-r.mq.Deliveries(queuename):

			var utxo model.UTXO
			err := json.Unmarshal(message.Body, &utxo)
			if err != nil {
				NackDeliveries([]amqp.Delivery{message})
				return heldUtxo{}, model.UTXO{}, err
			}
			outpoint := model.Outpoint{TxID: utxo.TxID, Vout: utxo.Vout}

			reason, detail, err := r.feeUtxoUsable(ctx, outpoint)
			if err != nil {
				NackDeliveries([]amqp.Delivery{message})
				return heldUtxo{}, model.UTXO{}, err
			}

			if reason != "" {
				telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, attribute.String("queue", string(queuename)), attribute.String("event", "dropped_"+reason))
				telemetry.Log(ctx).Warn("dropping unusable fee utxo", zap.String("utxo", utxo.UtxoID), zap.String("queue", string(queuename)), zap.String("reason", detail))
				r.AckDeliveries([]amqp.Delivery{message})
				continue
			}

			reservectx, cancel := context.WithTimeout(ctx, 2*time.Second)
			token, reserved, err := r.reservations.Reserve(reservectx, outpoint)
			cancel()
			if err != nil {
				telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, attribute.String("queue", string(queuename)), attribute.String("event", "taken_unreserved"))
				telemetry.Log(ctx).Warn("failed to reserve fee utxo, using it unreserved", zap.String("utxo", utxo.UtxoID), zap.Error(err))
				return heldUtxo{delivery: message, outpoint: outpoint}, utxo, nil
			}

			if !reserved {
				telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, attribute.String("queue", string(queuename)), attribute.String("event", "parked"))
				telemetry.Log(ctx).Warn("fee utxo held by another request, parking it", zap.String("utxo", utxo.UtxoID), zap.String("queue", string(queuename)))
				r.park(parkedUtxo{delivery: message, outpoint: outpoint})
				continue
			}

			telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, attribute.String("queue", string(queuename)), attribute.String("event", "taken"))
			return heldUtxo{delivery: message, outpoint: outpoint, token: token}, utxo, nil
		}
	}
}

const (
	UNUSABLE_SPENT         = "spent"
	UNUSABLE_FAILED_PARENT = "failed_parent"
)

func (r *RelayService) feeUtxoUsable(ctx context.Context, outpoint model.Outpoint) (string, string, error) {
	dbctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	spentBy, err := repo.GetSpendingTransaction(dbctx, r.db, outpoint)
	if err != nil {
		return "", "", err
	}
	if spentBy != "" {
		return UNUSABLE_SPENT, fmt.Sprintf("already spent by tx %s", spentBy), nil
	}

	parentFailed, err := repo.IsTransactionFailed(dbctx, r.db, outpoint.TxID)
	if err != nil {
		return "", "", err
	}
	if parentFailed {
		return UNUSABLE_FAILED_PARENT, fmt.Sprintf("its funding tx %s failed", outpoint.TxID), nil
	}

	return "", "", nil
}

func (r *RelayService) commitHeld(held []heldUtxo) {
	deliveries := make([]amqp.Delivery, 0, len(held))
	for _, h := range held {
		deliveries = append(deliveries, h.delivery)
	}
	r.AckDeliveries(deliveries)
	r.releaseHeld(held)
}

func (r *RelayService) returnHeld(held []heldUtxo) {
	deliveries := make([]amqp.Delivery, 0, len(held))
	for _, h := range held {
		deliveries = append(deliveries, h.delivery)
	}
	NackDeliveries(deliveries)
	r.releaseHeld(held)
}

func (r *RelayService) releaseHeld(held []heldUtxo) {
	for _, h := range held {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := r.reservations.Release(ctx, h.outpoint, h.token)
		cancel()
		if err != nil {
			zap.L().Warn("failed to release fee utxo reservation", zap.String("txid", h.outpoint.TxID), zap.Uint32("vout", h.outpoint.Vout), zap.Duration("expires_in", reservation.TTL), zap.Error(err))
		}
	}
}

func (r *RelayService) park(parked parkedUtxo) {
	r.parkedMu.Lock()
	defer r.parkedMu.Unlock()
	r.parked = append(r.parked, parked)
}

func (r *RelayService) watchParked(ctx context.Context) {
	ticker := time.NewTicker(PARKED_CHECK_INTERVAL)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.resolveParked(ctx)
		}
	}
}

func (r *RelayService) resolveParked(ctx context.Context) {
	r.parkedMu.Lock()
	parked := r.parked
	r.parked = nil
	r.parkedMu.Unlock()

	keep := make([]parkedUtxo, 0)
	for _, p := range parked {
		checkctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		reserved, err := r.reservations.IsReserved(checkctx, p.outpoint)
		cancel()
		if err != nil || reserved {
			keep = append(keep, p)
			continue
		}

		reason, _, err := r.feeUtxoUsable(ctx, p.outpoint)
		if err != nil {
			keep = append(keep, p)
			continue
		}

		queue := attribute.String("queue", string(rabbitmq.QueueFromRoutingKey(p.delivery.RoutingKey)))
		if reason != "" {
			telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, queue, attribute.String("event", "parked_dropped"))
			r.AckDeliveries([]amqp.Delivery{p.delivery})
			continue
		}
		telemetry.Count(ctx, telemetry.Metrics.FeeUtxoEvents, queue, attribute.String("event", "parked_requeued"))
		NackDeliveries([]amqp.Delivery{p.delivery})
	}

	if len(keep) > 0 {
		r.parkedMu.Lock()
		r.parked = append(r.parked, keep...)
		r.parkedMu.Unlock()
	}
}

func (r *RelayService) AckDeliveries(deliveries []amqp.Delivery) {
	consumed := make(map[rabbitmq.QueueName]int)
	for _, delivery := range deliveries {
		if err := delivery.Ack(false); err != nil {
			zap.L().Error("failed to ack utxo message", zap.Uint64("delivery_tag", delivery.DeliveryTag), zap.Error(err))
			continue
		}
		consumed[rabbitmq.QueueFromRoutingKey(delivery.RoutingKey)]++
	}

	if len(consumed) > 0 {
		r.recordDeficit(consumed)
		r.signalFunding()
	}
}

func NackDeliveries(deliveries []amqp.Delivery) {
	for _, delivery := range deliveries {
		if err := delivery.Nack(false, true); err != nil {
			zap.L().Error("failed to nack utxo message", zap.Uint64("delivery_tag", delivery.DeliveryTag), zap.Error(err))
		}
	}
}

func CalcuateQueues(amount uint64) []rabbitmq.QueueName {

	queuenames := make([]rabbitmq.QueueName, 0)
	currentAmount := amount

	for currentAmount > 0 {
		queuename := GetBestQueue(currentAmount)
		feeForQueue := feeForSize(INPUT_SIZE)
		queuenames = append(queuenames, queuename)
		if currentAmount+feeForQueue < rabbitmq.QueueToValue[queuename] {
			break
		}
		currentAmount = currentAmount + feeForQueue - rabbitmq.QueueToValue[queuename]
	}

	return queuenames

}

func GetBestQueue(amount uint64) rabbitmq.QueueName {
	margin := feeForSize(INPUT_SIZE)
	sum := uint64(0)

	for i, queuename := range rabbitmq.Queues {
		sum += rabbitmq.QueueToValue[queuename]
		if rabbitmq.QueueToValue[queuename]-margin >= amount || (sum-margin*uint64(i+1)) >= amount {
			return queuename
		}
	}

	return rabbitmq.Queues[len(rabbitmq.Queues)-1]
}
