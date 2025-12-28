package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/spf13/viper"

	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
)

const SAT_PER_KB = 100
const DEFAULT_FUND_AMOUNT = 1
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

func (r *RelayService) ingestUtxos(ctx context.Context) {

	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		log.Fatalf("critical: failed to fetch the address form the key manager: %v", err)
	}

	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		log.Fatalf("critical: faile to construct the lokcing script from addres: %v\n", err.Error())
	}

	for queuename := range r.fundingChan {
		dbctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		unxpentUtxos, err := repo.GetAllUnspentFundingUTXOs(dbctx, r.db)
		if err != nil {
			log.Printf("critical: failed to fetch funding utxo from db: %v\n", err)
			continue
		}
		cancel()

		if len(unxpentUtxos) == 0 {
			log.Println("warning: db is out of funding utxos")
			continue
		}

		tx := transaction.NewTransaction()

		sgh := sighash.AllForkID
		unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetPrivateKey(), &sgh)
		if err != nil {
			log.Printf("critical: failed to contstruct unlockingscript: %v\n", err.Error())
			continue
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
		fundAmount := viper.GetInt("fund_amount")
		if fundAmount == 0 {
			fundAmount = DEFAULT_FUND_AMOUNT
		}

		for range fundAmount {
			tx.AddOutput(&transaction.TransactionOutput{
				Satoshis:      rabbitmq.QueueToValue[queuename],
				LockingScript: lockingScript,
			})
			outputAmount += rabbitmq.QueueToValue[queuename]
		}

		//P2PKH size calc
		size := uint64(4 + 1 + (tx.InputCount() * INPUT_SIZE) + 1 + (tx.OutputCount() * OUTPUT_SIZE) + 4)
		fee := uint64((size*100 + 999) / 1000) //+999 does the ceil operation for us
		if inputAmount < (outputAmount + fee) {
			log.Printf("critical: failed to fund %s queue due to low funding utxo balance got: %d need %d\n", queuename, inputAmount, outputAmount+fee)
			continue
		}

		if inputAmount > (outputAmount + ((size + outputAmount*100 + 999) / 1000)) {
			size += OUTPUT_SIZE
			fee = uint64((size*100 + 999) / 1000)
			tx.AddOutput(&transaction.TransactionOutput{
				Satoshis:      (inputAmount - outputAmount - fee),
				LockingScript: lockingScript,
			})
		}

		err = tx.Sign()
		if err != nil {
			log.Println("critical: failed to sign the transaction ", err.Error())
			continue
		}

		extendedHex, err := tx.EFHex()
		if err != nil {
			log.Printf("ciritcal: failed to constrct extended hex from transaction for fee ingest for queue %s: %v\n", queuename, err.Error())
			continue
		}

		broadcastctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		brodcastResponse, err := r.broadcaster.Arc.BroadcastTx(broadcastctx, extendedHex, nil)
		if err != nil {
			log.Printf("ciritcal: failed to broadcast transaction for fee ingest for queue %s: %v\n", queuename, err.Error())
			continue
		}
		cancel()

		dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = repo.MarkFundingUTXOsAsSpent(dbctx, r.db, unxpentUtxos)
		if err != nil {
			log.Println("critical: failed to mark utxo as spent inconsistent state")
			continue
		}
		cancel()

		outputUtxos := make([]model.UTXO, 0, fundAmount)
		for i, output := range tx.Outputs {
			if i == fundAmount {
				break
			}
			outputUtxos = append(outputUtxos, model.UTXO{
				UtxoID: fmt.Sprintf("%s_%d", brodcastResponse.Txid, i),
				TxID:   brodcastResponse.Txid,
				Vout:   uint32(i),
				Amount: output.Satoshis,
			})
		}

		dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = repo.CreateFundingUTXOsIfNotExistsAndMarkAsSpent(dbctx, r.db, outputUtxos)
		if err != nil {
			log.Printf("critical: failed to record the chage in db  for fee transaction for queue %s:  %v", queuename, err.Error())
		}
		cancel()

		dbctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		if len(tx.Outputs) == fundAmount+1 {
			output := tx.Outputs[fundAmount]
			err = repo.CreateFundingUTXO(dbctx, r.db, &model.UTXO{
				UtxoID: fmt.Sprintf("%s_%d", brodcastResponse.Txid, fundAmount),
				TxID:   brodcastResponse.Txid,
				Vout:   uint32(fundAmount),
				Amount: output.Satoshis,
			})
			if err != nil {
				log.Printf("critical: failed to record the chage in db  for fee transaction for queue %s:  %v", queuename, err.Error())
			}
		}
		cancel()

		for i, output := range tx.Outputs {
			if i == fundAmount {
				log.Println("breaking after", i)
				break
			}
			log.Println("funding this:", i)
			log.Printf("sending this %d to queuname: %v\n", i, queuename)

			err := rabbitmq.Publish(r.ch, queuename, &model.UTXO{
				UtxoID: fmt.Sprintf("%s_%d", brodcastResponse.Txid, i),
				TxID:   brodcastResponse.Txid,
				Vout:   uint32(i),
				Amount: output.Satoshis,
			})
			if err != nil {
				log.Printf("critical: failed to ingest utxo amount in queue %s for fee transaction:  %v", queuename, err.Error())
			}

			log.Printf("done this %d to queuname: %v\n", i, queuename)
		}

		log.Printf("completed funding the queue: %v", queuename)
	}
}

func (r *RelayService) AddUtxo(txhex string) (string, error) {

	address, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		return "", err
	}
	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		return "", err
	}
	lockingScriptStr := hex.EncodeToString(lockingScript.Bytes())

	sgh := sighash.All | sighash.AnyOneCanPay | sighash.ForkID
	unlockingTemplate, err := p2pkh.Unlock(keymanager.KeyManager.GetPrivateKey(), &sgh)
	if err != nil {
		return "", err
	}

	tx, err := transaction.NewTransactionFromHex(txhex)
	if err != nil {
		return "", err
	}

	//intially we do need a utxo
	size := tx.Size()
	fee := uint64((size*100 + 999) / 1000)

	// we can predict the input size so we can calcuate the fund array with the fee
	//TODO: change the logic of funding to mutliqueue

	queunames := CalcuateQueues(fee)

	for _, queuename := range queunames {

		timeout := time.After(10 * time.Second)

		select {

		case <-timeout:
			return "", fmt.Errorf("timed out waiting utxo from queue %v, out of fee", queuename)

		case message := <-r.consumers[queuename]:
			var utxo model.UTXO
			err = json.Unmarshal(message.Body, &utxo)
			if err != nil {
				return "", err
			}
			err = tx.AddInputFrom(utxo.TxID, utxo.Vout, lockingScriptStr, utxo.Amount, unlockingTemplate)
			if err != nil {
				return "", err
			}

		}
	}

	err = tx.Sign()
	if err != nil {
		return "", err
	}

	return tx.EFHex()
}

func (r *RelayService) StartQueueMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	fundAmount := viper.GetInt("fund_amount")
	if fundAmount == 0 {
		fundAmount = DEFAULT_FUND_AMOUNT
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			for queuename, queue := range r.queues {
				if queue.Messages < fundAmount {
					r.fundingChan <- queuename
				}
			}
		}
	}
}

func CalcuateQueues(amount uint64) []rabbitmq.QueueName {

	queuenames := make([]rabbitmq.QueueName, 0)
	currentAmount := amount

	for currentAmount > 0 {
		queuename := GetBestQueue(currentAmount)
		feeForQueue := uint64((INPUT_SIZE*100 + 999) / 1000)
		queuenames = append(queuenames, queuename)
		if currentAmount+feeForQueue < rabbitmq.QueueToValue[queuename] {
			break
		}
		currentAmount = currentAmount + feeForQueue - rabbitmq.QueueToValue[queuename]
	}

	return queuenames

}

func GetBestQueue(amount uint64) rabbitmq.QueueName {
	margin := uint64((INPUT_SIZE*100 + 999) / 1000)
	sum := uint64(0)

	for i, queuename := range rabbitmq.Queues {
		sum += rabbitmq.QueueToValue[queuename]
		if rabbitmq.QueueToValue[queuename]-margin >= amount || (sum-margin*uint64(i+1)) >= amount {
			return queuename
		}
	}

	return rabbitmq.Queues[len(rabbitmq.Queues)-1]
}
