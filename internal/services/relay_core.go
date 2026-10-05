package services

import (
	"context"
	"database/sql"
	"encoding/hex"
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

type Reservations interface {
	Reserve(ctx context.Context, outpoint model.Outpoint) (string, bool, error)
	Release(ctx context.Context, outpoint model.Outpoint, token string) error
	IsReserved(ctx context.Context, outpoint model.Outpoint) (bool, error)
	AcquireLock(ctx context.Context, name string, ttl time.Duration) (string, bool, error)
	ReleaseLock(ctx context.Context, name string, token string) error
}

type heldUtxo struct {
	delivery amqp.Delivery
	outpoint model.Outpoint
	token    string
}

type parkedUtxo struct {
	delivery amqp.Delivery
	outpoint model.Outpoint
}

type RelayService struct {
	db           *sql.DB
	broadcaster  *broadcaster.Broadcaster
	mq           UtxoQueue
	reservations Reservations
	fundingChan  chan struct{}
	deficitMu    sync.Mutex
	deficit      map[rabbitmq.QueueName]int
	parkedMu     sync.Mutex
	parked       []parkedUtxo
	syncConfig   SyncConfig
}

func NewRelayService(db *sql.DB, broadcaster *broadcaster.Broadcaster, mq UtxoQueue, reservations Reservations) *RelayService {
	return &RelayService{
		db:           db,
		broadcaster:  broadcaster,
		mq:           mq,
		reservations: reservations,
		fundingChan:  make(chan struct{}, 1),
		deficit:      make(map[rabbitmq.QueueName]int),
		syncConfig:   LoadSyncConfig(),
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

	held, err := s.AddUtxo(ctx, tx)
	if err != nil {
		return nil, err
	}

	if err := verifyScripts(tx, true); err != nil {
		s.returnHeld(held)
		return nil, err
	}
	if err := checkFee(tx); err != nil {
		s.returnHeld(held)
		return nil, err
	}

	return s.submit(ctx, tx, held)
}

func (s *RelayService) submit(ctx context.Context, tx *transaction.Transaction, held []heldUtxo) (*model.Transaction, error) {
	stored, err := s.store(ctx, tx)
	if err != nil {
		s.returnHeld(held)
		return nil, err
	}
	s.commitHeld(held)

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

var ErrTransactionNotFound = errors.New("transaction not found")

func (s *RelayService) GetTransaction(ctx context.Context, txID string) (*model.Transaction, error) {
	if len(txID) != 64 {
		return nil, fmt.Errorf("%w: txid must be 64 hex characters", ErrInvalidTransaction)
	}
	if _, err := hex.DecodeString(txID); err != nil {
		return nil, fmt.Errorf("%w: txid must be 64 hex characters", ErrInvalidTransaction)
	}

	tx, err := repo.GetTransaction(ctx, s.db, txID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTransactionNotFound
	}
	return tx, err
}

func (s *RelayService) GetFundingAddress() (string, error) {
	addr, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		return "", err
	}
	return addr.AddressString, nil
}
