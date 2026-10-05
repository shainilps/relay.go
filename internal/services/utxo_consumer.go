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

	target := fundTarget()
	startup := make(map[rabbitmq.QueueName]int)
	for queuename, queue := range r.queues {
		if queue.Messages < target {
			startup[queuename] = target - queue.Messages
		}
	}
	log.Printf("startup funding deficit: %v\n", startup)
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
		if len(deficit) == 0 {
			continue
		}

		unfunded := r.fundQueues(ctx, deficit, lockingScript)
		if len(unfunded) > 0 {
			r.recordDeficit(unfunded)
			retry = time.After(FUNDING_RETRY_INTERVAL)
			log.Printf("warning: unfunded queues %v, retrying in %s\n", unfunded, FUNDING_RETRY_INTERVAL)
		}
	}
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

	//P2PKH size calc
	size := uint64(4 + 1 + (tx.InputCount() * INPUT_SIZE) + 1 + (tx.OutputCount() * OUTPUT_SIZE) + 4)
	fee := feeForSize(int(size))
	if inputAmount < (outputAmount + fee) {
		log.Printf("critical: failed to fund queues %v due to low funding utxo balance got: %d need %d\n", deficit, inputAmount, outputAmount+fee)
		return deficit
	}

	if inputAmount > (outputAmount + ((size + outputAmount*100 + 999) / 1000)) {
		size += OUTPUT_SIZE
		fee = feeForSize(int(size))
		tx.AddOutput(&transaction.TransactionOutput{
			Satoshis:      (inputAmount - outputAmount - fee),
			LockingScript: lockingScript,
		})
	}

	err = tx.Sign()
	if err != nil {
		log.Println("critical: failed to sign the transaction ", err.Error())
		return deficit
	}

	extendedHex, err := tx.EFHex()
	if err != nil {
		log.Printf("ciritcal: failed to constrct extended hex from transaction for fee ingest for queues %v: %v\n", deficit, err.Error())
		return deficit
	}

	broadcastctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	brodcastResponse, err := r.broadcaster.Arc.BroadcastTx(broadcastctx, extendedHex, nil)
	cancel()
	if err != nil {
		log.Printf("ciritcal: failed to broadcast transaction for fee ingest for queues %v: %v\n", deficit, err.Error())
		return deficit
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = repo.MarkFundingUTXOsAsSpent(dbctx, r.db, unxpentUtxos)
	cancel()
	if err != nil {
		log.Println("critical: failed to mark utxo as spent inconsistent state")
	}

	outputUtxos := make([]model.UTXO, 0, fundCount)
	for i := range fundCount {
		outputUtxos = append(outputUtxos, model.UTXO{
			UtxoID: fmt.Sprintf("%s_%d", brodcastResponse.Txid, i),
			TxID:   brodcastResponse.Txid,
			Vout:   uint32(i),
			Amount: tx.Outputs[i].Satoshis,
		})
	}

	dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	err = repo.CreateFundingUTXOsIfNotExistsAndMarkAsSpent(dbctx, r.db, outputUtxos)
	cancel()
	if err != nil {
		log.Printf("critical: failed to record the queue utxos in db for fee transaction %s: %v", brodcastResponse.Txid, err.Error())
	}

	if len(tx.Outputs) == fundCount+1 {
		dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = repo.CreateFundingUTXO(dbctx, r.db, &model.UTXO{
			UtxoID: fmt.Sprintf("%s_%d", brodcastResponse.Txid, fundCount),
			TxID:   brodcastResponse.Txid,
			Vout:   uint32(fundCount),
			Amount: tx.Outputs[fundCount].Satoshis,
		})
		cancel()
		if err != nil {
			log.Printf("critical: failed to record the change in db for fee transaction %s: %v", brodcastResponse.Txid, err.Error())
		}
	}

	unfunded := make(map[rabbitmq.QueueName]int)
	for i, utxo := range outputUtxos {
		queuename := outputQueues[i]
		err := rabbitmq.Publish(r.ch, queuename, &utxo)
		if err != nil {
			log.Printf("critical: failed to ingest utxo %s in queue %s: %v", utxo.UtxoID, queuename, err.Error())
			unfunded[queuename]++
		}
	}

	log.Printf("funded queues %v with tx %s\n", deficit, brodcastResponse.Txid)
	return unfunded
}

func (r *RelayService) AddUtxo(tx *transaction.Transaction) ([]amqp.Delivery, error) {

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

		timeout := time.After(10 * time.Second)

		select {

		case <-timeout:
			return fail(fmt.Errorf("%w: timed out waiting utxo from queue %v", ErrOutOfFee, queuename))

		case message, ok := <-r.consumers[queuename]:
			if !ok {
				return fail(fmt.Errorf("consumer for queue %v is closed", queuename))
			}
			deliveries = append(deliveries, message)

			var utxo model.UTXO
			err = json.Unmarshal(message.Body, &utxo)
			if err != nil {
				return fail(err)
			}
			err = tx.AddInputFrom(utxo.TxID, utxo.Vout, lockingScriptStr, utxo.Amount, unlockingTemplate)
			if err != nil {
				return fail(err)
			}

		}
	}

	err = tx.Sign()
	if err != nil {
		return fail(err)
	}

	return deliveries, nil
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
