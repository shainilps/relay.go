package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/db/dbtest"
	"github.com/shainilps/relay/internal/db/repo"
	"github.com/shainilps/relay/internal/model"
	"github.com/shainilps/relay/internal/rabbitmq"
)

type fakeAcknowledger struct {
	failTags map[uint64]bool
}

func (f *fakeAcknowledger) Ack(tag uint64, multiple bool) error {
	if f.failTags[tag] {
		return errors.New("channel closed")
	}
	return nil
}

func (f *fakeAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	return nil
}

func (f *fakeAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

type fakeQueue struct {
	deliveries map[rabbitmq.QueueName]chan amqp.Delivery
}

func (f *fakeQueue) Deliveries(queue rabbitmq.QueueName) <-chan amqp.Delivery {
	return f.deliveries[queue]
}

func (f *fakeQueue) Queues() map[rabbitmq.QueueName]amqp.Queue {
	return nil
}

func (f *fakeQueue) Publish(ctx context.Context, queue rabbitmq.QueueName, utxo *model.UTXO) error {
	return nil
}

func newFundingTestService() *RelayService {
	return NewRelayService(nil, nil, &fakeQueue{})
}

func TestAckDeliveriesRecordsDeficit(t *testing.T) {
	r := newFundingTestService()
	acknowledger := &fakeAcknowledger{failTags: map[uint64]bool{3: true}}

	r.AckDeliveries([]amqp.Delivery{
		{Acknowledger: acknowledger, DeliveryTag: 1, RoutingKey: string(rabbitmq.QUEUE_50)},
		{Acknowledger: acknowledger, DeliveryTag: 2, RoutingKey: string(rabbitmq.QUEUE_50)},
		{Acknowledger: acknowledger, DeliveryTag: 3, RoutingKey: string(rabbitmq.QUEUE_100)},
		{Acknowledger: acknowledger, DeliveryTag: 4, RoutingKey: string(rabbitmq.QUEUE_200)},
	})
	r.AckDeliveries([]amqp.Delivery{
		{Acknowledger: acknowledger, DeliveryTag: 5, RoutingKey: string(rabbitmq.QUEUE_200)},
	})

	select {
	case <-r.fundingChan:
	default:
		t.Fatal("expected a funding signal")
	}
	select {
	case <-r.fundingChan:
		t.Fatal("expected signals to collapse into one")
	default:
	}

	deficit := r.takeDeficit()
	expected := map[rabbitmq.QueueName]int{rabbitmq.QUEUE_50: 2, rabbitmq.QUEUE_200: 2}
	if len(deficit) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, deficit)
	}
	for queuename, count := range expected {
		if deficit[queuename] != count {
			t.Fatalf("expected %v, got %v", expected, deficit)
		}
	}

	if left := r.takeDeficit(); len(left) != 0 {
		t.Fatalf("expected deficit to be cleared, got %v", left)
	}
}

func TestAckDeliveriesWithNothingAckedDoesNotSignal(t *testing.T) {
	r := newFundingTestService()
	acknowledger := &fakeAcknowledger{failTags: map[uint64]bool{1: true}}

	r.AckDeliveries([]amqp.Delivery{{Acknowledger: acknowledger, DeliveryTag: 1, RoutingKey: string(rabbitmq.QUEUE_50)}})
	r.AckDeliveries(nil)

	select {
	case <-r.fundingChan:
		t.Fatal("expected no funding signal")
	default:
	}
}

type recordingAcknowledger struct {
	acked  []uint64
	nacked []uint64
}

func (a *recordingAcknowledger) Ack(tag uint64, multiple bool) error {
	a.acked = append(a.acked, tag)
	return nil
}

func (a *recordingAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	a.nacked = append(a.nacked, tag)
	return nil
}

func (a *recordingAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

func utxoDelivery(t *testing.T, acknowledger amqp.Acknowledger, tag uint64, redelivered bool, utxo model.UTXO) amqp.Delivery {
	t.Helper()
	body, err := json.Marshal(utxo)
	if err != nil {
		t.Fatal(err)
	}
	return amqp.Delivery{Acknowledger: acknowledger, DeliveryTag: tag, Redelivered: redelivered, RoutingKey: string(rabbitmq.QUEUE_50), Body: body}
}

func TestTakeUtxoDropsSpentUtxo(t *testing.T) {
	ctx := context.Background()
	db := dbtest.New(t)

	spent := model.UTXO{UtxoID: "funding_0", TxID: "funding", Vout: 0, Amount: 50}
	unspentRedelivered := model.UTXO{UtxoID: "funding_1", TxID: "funding", Vout: 1, Amount: 50}

	if err := repo.CreateTransaction(ctx, db, &model.Transaction{TxID: "stored", TxHex: "00", Network: model.MAIN}, []model.Outpoint{{TxID: spent.TxID, Vout: spent.Vout}}); err != nil {
		t.Fatal(err)
	}

	acknowledger := &recordingAcknowledger{}
	queue := make(chan amqp.Delivery, 2)
	queue <- utxoDelivery(t, acknowledger, 1, false, spent)
	queue <- utxoDelivery(t, acknowledger, 2, true, unspentRedelivered)

	r := NewRelayService(db, nil, &fakeQueue{deliveries: map[rabbitmq.QueueName]chan amqp.Delivery{rabbitmq.QUEUE_50: queue}})

	message, utxo, err := r.takeUtxo(ctx, rabbitmq.QUEUE_50)
	if err != nil {
		t.Fatal(err)
	}
	if message.DeliveryTag != 2 || utxo.UtxoID != unspentRedelivered.UtxoID {
		t.Fatalf("expected the unspent redelivered utxo, got tag %d utxo %s", message.DeliveryTag, utxo.UtxoID)
	}
	if len(acknowledger.acked) != 1 || acknowledger.acked[0] != 1 || len(acknowledger.nacked) != 0 {
		t.Fatalf("expected only the spent utxo to be acked, got acked %v nacked %v", acknowledger.acked, acknowledger.nacked)
	}
	if deficit := r.takeDeficit(); deficit[rabbitmq.QUEUE_50] != 1 {
		t.Fatalf("expected the dropped utxo to be refunded, got %v", deficit)
	}
}
