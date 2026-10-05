package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/spf13/viper"

	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
)

const SAT_PER_KB = 100
const DEFAULT_FUND_AMOUNT = 1
const FUNDING_RETRY_INTERVAL = 30 * time.Second
const INPUT_SIZE = 149 // this is can be 149 also because DER singature can be 32/33
const OUTPUT_SIZE = 34

var MIN_CHANGE = feeForSize(INPUT_SIZE)

func (r *RelayService) StartEngine(ctx context.Context) {
	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		log.Fatalf("failed to fetch address from key manager %v", err.Error())
	}

	reqctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	log.Println("address being from utxo: ", address.AddressString)
	utxosets, err := r.broadcaster.Explorer.GetUtxosForAddress(reqctx, address.AddressString)
	if err != nil {
		log.Printf("warning: failed to get the any funding utxo: %v\n", err.Error())
	}
	cancel()
	if utxosets != nil && len(utxosets.Result) != 0 {
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

		log.Println("funding utxo: ", fundingUtxo)
		dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = repo.CreateFundingUTXOsIfNotExists(dbctx, r.db, fundingUtxo)
		if err != nil {
			log.Printf("warning: failed to insert utxo records on db: %v\n", err.Error())
		}
		cancel()
	}

	r.ingestUtxos(ctx)
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
		log.Fatalf("critical: failed to fetch the address form the key manager: %v", err)
	}

	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		log.Fatalf("critical: faile to construct the lokcing script from addres: %v\n", err.Error())
	}

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	unpublished, err := repo.GetUnpublishedQueueUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		log.Fatalf("critical: failed to load unpublished queue utxos: %v", err)
	}
	pending := make(map[rabbitmq.QueueName]int)
	for _, utxo := range unpublished {
		pending[rabbitmq.QueueName(utxo.Queue)]++
	}

	target := fundTarget()
	startup := make(map[rabbitmq.QueueName]int)
	for queuename, queue := range r.queues {
		if missing := target - queue.Messages - pending[queuename]; missing > 0 {
			startup[queuename] = missing
		}
	}
	log.Printf("startup funding deficit: %v, unpublished: %v\n", startup, pending)
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

		deficit := r.takeDeficit()
		if len(deficit) > 0 {
			unfunded := r.fundQueues(ctx, deficit, lockingScript)
			if len(unfunded) > 0 {
				r.recordDeficit(unfunded)
				retry = time.After(FUNDING_RETRY_INTERVAL)
				log.Printf("warning: unfunded queues %v, retrying in %s\n", unfunded, FUNDING_RETRY_INTERVAL)
			}
		}

		if !r.publishPending(ctx) && retry == nil {
			retry = time.After(FUNDING_RETRY_INTERVAL)
			log.Printf("warning: queue utxos left unpublished, retrying in %s\n", FUNDING_RETRY_INTERVAL)
		}
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
	if inputAmount < outputAmount+feeWithChange+MIN_CHANGE {
		return 0, inputAmount - outputAmount, true
	}

	return inputAmount - outputAmount - feeWithChange, feeWithChange, true
}

func (r *RelayService) fundQueues(ctx context.Context, deficit map[rabbitmq.QueueName]int, lockingScript *script.Script) map[rabbitmq.QueueName]int {

	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	unxpentUtxos, err := repo.GetAllUnspentFundingUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		log.Printf("critical: failed to fetch funding utxo from db: %v\n", err)
		return deficit
	}

	if len(unxpentUtxos) == 0 {
		log.Println("warning: db is out of funding utxos")
		return deficit
	}

	tx := transaction.NewTransaction()

	sgh := sighash.AllForkID
	unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetPrivateKey(), &sgh)
	if err != nil {
		log.Printf("critical: failed to contstruct unlockingscript: %v\n", err.Error())
		return deficit
	}

	var inputAmount uint64
	for _, utxo := range unxpentUtxos {
		err = tx.AddInputFrom(utxo.TxID, utxo.Vout, hex.EncodeToString(lockingScript.Bytes()), utxo.Amount, unlockingTemplate)
		if err != nil {
			log.Printf("critical: failed to add utxo to transaction: %v\n", err.Error())
		}
		inputAmount += utxo.Amount
	}

	var outputAmount uint64
	outputQueues := make([]rabbitmq.QueueName, 0)
	for _, queuename := range rabbitmq.Queues {
		for range deficit[queuename] {
			tx.AddOutput(&transaction.TransactionOutput{
				Satoshis:      rabbitmq.QueueToValue[queuename],
				LockingScript: lockingScript,
			})
			outputAmount += rabbitmq.QueueToValue[queuename]
			outputQueues = append(outputQueues, queuename)
		}
	}
	fundCount := len(outputQueues)

	change, fee, ok := planChange(inputAmount, outputAmount, tx.InputCount(), tx.OutputCount())
	if !ok {
		log.Printf("critical: failed to fund queues %v due to low funding utxo balance got: %d need %d\n", deficit, inputAmount, outputAmount+fee)
		return deficit
	}

	if change > 0 {
		tx.AddOutput(&transaction.TransactionOutput{
			Satoshis:      change,
			LockingScript: lockingScript,
		})
	}

	err = tx.Sign()
	if err != nil {
		log.Println("critical: failed to sign the transaction ", err.Error())
		return deficit
	}

	if err := verifyScripts(tx, true); err != nil {
		log.Printf("critical: funding transaction failed validation: %v\n", err)
		return deficit
	}

	extendedHex, err := tx.EFHex()
	if err != nil {
		log.Printf("ciritcal: failed to constrct extended hex from transaction for fee ingest for queues %v: %v\n", deficit, err.Error())
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
		Network:       model.Network(viper.GetString("app.network")),
		NextAttemptAt: time.Now().Add(r.syncConfig.RebroadcastInterval).Unix(),
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = repo.StoreFundingTransaction(dbctx, r.db, fundingTx, unxpentUtxos, queueUtxos, changeUtxo)
	cancel()
	if err != nil {
		log.Printf("critical: failed to store funding transaction %s: %v\n", txID, err)
		return deficit
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	stored, err := repo.GetTransaction(dbctx, r.db, txID)
	cancel()
	if err != nil {
		log.Printf("critical: failed to load stored funding transaction %s, leaving it to the syncer: %v\n", txID, err)
		return nil
	}

	if _, err := r.attemptBroadcast(ctx, stored); err != nil {
		log.Printf("critical: failed to record broadcast of funding transaction %s: %v\n", txID, err)
	}

	log.Printf("funded queues %v with tx %s\n", deficit, txID)
	return nil
}

func (r *RelayService) publishPending(ctx context.Context) bool {
	dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pending, err := repo.GetUnpublishedQueueUTXOs(dbctx, r.db)
	cancel()
	if err != nil {
		log.Printf("critical: failed to load unpublished queue utxos: %v\n", err)
		return false
	}

	for _, utxo := range pending {
		publishctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := rabbitmq.Publish(publishctx, r.ch, rabbitmq.QueueName(utxo.Queue), &utxo.UTXO)
		cancel()
		if err != nil {
			log.Printf("critical: failed to publish utxo %s to queue %s: %v\n", utxo.UtxoID, utxo.Queue, err)
			return false
		}

		dbctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = repo.MarkQueueUTXOPublished(dbctx, r.db, utxo.UtxoID)
		cancel()
		if err != nil {
			log.Printf("critical: failed to mark utxo %s as published: %v\n", utxo.UtxoID, err)
			return false
		}
	}

	return true
}

func (r *RelayService) AddUtxo(ctx context.Context, tx *transaction.Transaction) ([]amqp.Delivery, error) {

	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		return nil, err
	}
	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		return nil, err
	}
	lockingScriptStr := hex.EncodeToString(lockingScript.Bytes())

	sgh := sighash.All | sighash.AnyOneCanPay | sighash.ForkID
	unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetPrivateKey(), &sgh)
	if err != nil {
		return nil, err
	}

	//intially we do need a utxo
	fee := feeForSize(tx.Size())

	// we can predict the input size so we can calcuate the fund array with the fee
	//TODO: change the logic of funding to mutliqueue

	queunames := CalcuateQueues(fee)

	deliveries := make([]amqp.Delivery, 0, len(queunames))
	fail := func(err error) ([]amqp.Delivery, error) {
		NackDeliveries(deliveries)
		return nil, err
	}

	for _, queuename := range queunames {
		message, utxo, err := r.takeUtxo(ctx, queuename)
		if err != nil {
			return fail(err)
		}
		deliveries = append(deliveries, message)

		err = tx.AddInputFrom(utxo.TxID, utxo.Vout, lockingScriptStr, utxo.Amount, unlockingTemplate)
		if err != nil {
			return fail(err)
		}
	}

	err = tx.Sign()
	if err != nil {
		return fail(err)
	}

	return deliveries, nil
}

func (r *RelayService) takeUtxo(ctx context.Context, queuename rabbitmq.QueueName) (amqp.Delivery, model.UTXO, error) {
	timeout := time.After(10 * time.Second)

	for {
		select {

		case <-timeout:
			return amqp.Delivery{}, model.UTXO{}, fmt.Errorf("%w: timed out waiting utxo from queue %v", ErrOutOfFee, queuename)

		case message, ok := <-r.consumers[queuename]:
			if !ok {
				return amqp.Delivery{}, model.UTXO{}, fmt.Errorf("consumer for queue %v is closed", queuename)
			}

			var utxo model.UTXO
			err := json.Unmarshal(message.Body, &utxo)
			if err != nil {
				NackDeliveries([]amqp.Delivery{message})
				return amqp.Delivery{}, model.UTXO{}, err
			}

			dbctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			spentBy, err := repo.GetSpendingTransaction(dbctx, r.db, model.Outpoint{TxID: utxo.TxID, Vout: utxo.Vout})
			cancel()
			if err != nil {
				NackDeliveries([]amqp.Delivery{message})
				return amqp.Delivery{}, model.UTXO{}, err
			}

			if spentBy == "" {
				return message, utxo, nil
			}

			log.Printf("warning: dropping utxo %s from queue %s, already spent by tx %s\n", utxo.UtxoID, queuename, spentBy)
			r.AckDeliveries([]amqp.Delivery{message})
		}
	}
}

func (r *RelayService) AckDeliveries(deliveries []amqp.Delivery) {
	consumed := make(map[rabbitmq.QueueName]int)
	for _, delivery := range deliveries {
		if err := delivery.Ack(false); err != nil {
			log.Printf("critical: failed to ack utxo message %d: %v\n", delivery.DeliveryTag, err)
			continue
		}
		consumed[rabbitmq.QueueName(delivery.RoutingKey)]++
	}

	if len(consumed) > 0 {
		r.recordDeficit(consumed)
		r.signalFunding()
	}
}

func NackDeliveries(deliveries []amqp.Delivery) {
	for _, delivery := range deliveries {
		if err := delivery.Nack(false, true); err != nil {
			log.Printf("critical: failed to nack utxo message %d: %v\n", delivery.DeliveryTag, err)
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
