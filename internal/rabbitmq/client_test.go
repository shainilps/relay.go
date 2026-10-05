package rabbitmq

import (
	"testing"

	"github.com/shainilps/relay/internal/model"
)

func TestQueueNames(t *testing.T) {
	mainClient := &Client{network: model.MAIN}
	testClient := &Client{network: model.TEST}

	if got := mainClient.physicalName(QUEUE_50); got != "MAIN.QUEUE_50" {
		t.Fatalf("expected MAIN.QUEUE_50, got %s", got)
	}
	if got := testClient.physicalName(QUEUE_50); got != "TEST.QUEUE_50" {
		t.Fatalf("expected TEST.QUEUE_50, got %s", got)
	}

	for routingKey, expected := range map[string]QueueName{
		"MAIN.QUEUE_50":   QUEUE_50,
		"TEST.QUEUE_1600": QUEUE_1600,
		"QUEUE_100":       QUEUE_100,
	} {
		if got := QueueFromRoutingKey(routingKey); got != expected {
			t.Fatalf("expected %s for %s, got %s", expected, routingKey, got)
		}
	}
}
