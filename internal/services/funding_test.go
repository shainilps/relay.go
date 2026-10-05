package services

import (
	"errors"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
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

func newFundingTestService() *RelayService {
	return NewRelayService(nil, nil, nil, nil, nil)
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
