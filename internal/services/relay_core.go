package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/spf13/viper"
)

type UtxoQueue interface {
	Deliveries(queue rabbitmq.QueueName) <-chan amqp.Delivery
	Queues() map[rabbitmq.QueueName]amqp.Queue
	Publish(ctx context.Context, queue rabbitmq.QueueName, utxo *model.UTXO) error
}

type RelayService struct {
	db          *sql.DB
	broadcaster *broadcaster.Broadcaster
	mq          UtxoQueue
	fundingChan chan struct{}
	deficitMu   sync.Mutex
	deficit     map[rabbitmq.QueueName]int
	syncConfig  SyncConfig
}

func NewRelayService(db *sql.DB, broadcaster *broadcaster.Broadcaster, mq UtxoQueue) *RelayService {
	return &RelayService{
		db:          db,
		broadcaster: broadcaster,
		mq:          mq,
		fundingChan: make(chan struct{}, 1),
		deficit:     make(map[rabbitmq.QueueName]int),
		syncConfig:  LoadSyncConfig(),
	}
}

func parseTransaction(txHex string) (*transaction.Transaction, error) {
	tx, err := transaction.NewTransactionFromHex(txHex)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}
	return tx, nil
}

func (s *RelayService) Broadcast(ctx context.Context, txHex string) (*model.Transaction, error) {
	tx, err := parseTransaction(txHex)
	if err != nil {
		return nil, err
	}

	if err := verifyScripts(tx, false); err != nil {
		return nil, err
	}
	if hasAllSources(tx) {
		if err := checkFee(tx); err != nil {
			return nil, err
		}
	}

	return s.submit(ctx, tx, nil)
}

func (s *RelayService) FundAndBroadcast(ctx context.Context, txHex string) (*model.Transaction, error) {
	tx, err := parseTransaction(txHex)
	if err != nil {
		return nil, err
	}

	if err := verifyScripts(tx, true); err != nil {
		return nil, err
	}

	deliveries, err := s.AddUtxo(ctx, tx)
	if err != nil {
		return nil, err
	}

	if err := verifyScripts(tx, true); err != nil {
		NackDeliveries(deliveries)
		return nil, err
	}
	if err := checkFee(tx); err != nil {
		NackDeliveries(deliveries)
		return nil, err
	}

	return s.submit(ctx, tx, deliveries)
}

func (s *RelayService) submit(ctx context.Context, tx *transaction.Transaction, deliveries []amqp.Delivery) (*model.Transaction, error) {
	stored, err := s.store(ctx, tx)
	if err != nil {
		NackDeliveries(deliveries)
		return nil, err
	}
	s.AckDeliveries(deliveries)

	return s.broadcastStored(ctx, stored)
}

func (s *RelayService) store(ctx context.Context, tx *transaction.Transaction) (*model.Transaction, error) {
	txHex := tx.Hex()
	if hasAllSources(tx) {
		efHex, err := tx.EFHex()
		if err != nil {
			return nil, err
		}
		txHex = efHex
	}

	inputs := make([]model.Outpoint, 0, len(tx.Inputs))
	for _, input := range tx.Inputs {
		inputs = append(inputs, model.Outpoint{TxID: input.SourceTXID.String(), Vout: input.SourceTxOutIndex})
	}

	txID := tx.TxID().String()
	err := repo.CreateTransaction(ctx, s.db, &model.Transaction{
		TxID:          txID,
		TxHex:         txHex,
		Network:       model.Network(viper.GetString("app.network")),
		NextAttemptAt: time.Now().Add(s.syncConfig.RebroadcastInterval).Unix(),
	}, inputs)
	var doubleSpendErr *repo.DoubleSpendError
	if errors.As(err, &doubleSpendErr) {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}
	if err != nil {
		return nil, err
	}

	return repo.GetTransaction(ctx, s.db, txID)
}

func (s *RelayService) broadcastStored(ctx context.Context, stored *model.Transaction) (*model.Transaction, error) {
	if stored.Status == model.PENDING && stored.Attempts == 0 {
		if _, err := s.attemptBroadcast(ctx, stored); err != nil {
			return nil, err
		}
	}

	dbctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return repo.GetTransaction(dbctx, s.db, stored.TxID)
}

func (s *RelayService) GetFundingAddress() (string, error) {
	addr, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		return "", err
	}
	return addr.AddressString, nil
}
