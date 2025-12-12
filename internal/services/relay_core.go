package services

import (
	"context"
	"database/sql"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
)

type RelayService struct {
	ch          *amqp.Channel
	db          *sql.DB
	broadcaster *broadcaster.Broadcaster
	consumers   map[rabbitmq.QueueName]<-chan amqp.Delivery
	queues      map[rabbitmq.QueueName]amqp.Queue
	fundingChan chan rabbitmq.QueueName
}

func NewRelayService(db *sql.DB, ch *amqp.Channel, broadcaster *broadcaster.Broadcaster, consumers map[rabbitmq.QueueName]<-chan amqp.Delivery, queues map[rabbitmq.QueueName]amqp.Queue, fundingChan chan rabbitmq.QueueName) *RelayService {
	return &RelayService{
		ch, db, broadcaster, consumers, queues, fundingChan,
	}
}

func (s *RelayService) Broadcast(ctx context.Context, txHex string) (*broadcaster.BroadcastTxResponse, error) {
	response, err := s.broadcaster.Arc.BroadcastTx(ctx, txHex, nil)
	if err != nil {
		return nil, err
	}
	err = repo.CreateTransaction(ctx, s.db, &model.Transaction{
		TxID:    response.Txid,
		TxHex:   response.BlockHash,
		Height:  response.BlockHeight,
		Network: model.MAIN,
		Status:  model.UNSYNCED,
	})
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (s *RelayService) FundAndBroadcast(ctx context.Context, txHex string) (*broadcaster.BroadcastTxResponse, error) {
	txHexWithFee, err := s.AddUtxo(txHex)
	if err != nil {
		return nil, err
	}
	return s.Broadcast(ctx, txHexWithFee)
}

func (s *RelayService) GetFundingAddress() (string, error) {
	addr, err := keymanager.KeyManager.GetAddress()
	if err != nil {
		return "", err
	}
	return addr.AddressString, nil
}
