package services

import (
	"testing"

	"github.com/shainilps/relay/internal/rabbitmq"
)

func TestCalculateQueues(t *testing.T) {

	tests := []struct {
		name     string
		amount   uint64
		expected []rabbitmq.QueueName
	}{
		{
			name:     "should just get the 100",
			amount:   49,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_100},
		},
		{
			name:     "should get the 50",
			amount:   30,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_50},
		},
		{
			name:     "should get the 100",
			amount:   78,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_100},
		},
		{
			name:     "should get 100 + 50",
			amount:   110,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_100, rabbitmq.QUEUE_50},
		},
		{
			name:     "should get 200",
			amount:   180,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_200},
		},
		{
			name:     "should get 200 + 50",
			amount:   220,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_200, rabbitmq.QUEUE_50},
		},
		{
			name:     "should get 400",
			amount:   350,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_400},
		},
		{
			name:     "should get 400 + 100",
			amount:   450,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_400, rabbitmq.QUEUE_100},
		},
		{
			name:     "should get 800",
			amount:   700,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_800},
		},
		{
			name:     "should get 800 + 200 + 50",
			amount:   1000,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_800, rabbitmq.QUEUE_200, rabbitmq.QUEUE_50},
		},
		{
			name:     "should get 1600",
			amount:   1500,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_1600},
		},
		{
			name:     "should get 1600 + 400",
			amount:   1900,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_1600, rabbitmq.QUEUE_400},
		},
		{
			name:     "should get 12800 for large amount",
			amount:   13000,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_12800, rabbitmq.QUEUE_200, rabbitmq.QUEUE_50},
		},
		{
			name:     "should get multiple queues for amount exceeding 12800",
			amount:   20000,
			expected: []rabbitmq.QueueName{rabbitmq.QUEUE_12800, rabbitmq.QUEUE_6400, rabbitmq.QUEUE_800, rabbitmq.QUEUE_100},
		},
		{
			name:   "should get all top queues for very large amount",
			amount: 50000,
			expected: []rabbitmq.QueueName{
				rabbitmq.QUEUE_12800,
				rabbitmq.QUEUE_12800,
				rabbitmq.QUEUE_12800,
				rabbitmq.QUEUE_6400,
				rabbitmq.QUEUE_3200,
				rabbitmq.QUEUE_1600,
				rabbitmq.QUEUE_400,
				rabbitmq.QUEUE_100,
				rabbitmq.QUEUE_50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcuateQueues(tt.amount)

			if len(got) != len(tt.expected) {
				t.Fatalf(
					"expected %d queues, got %d (%v)",
					len(tt.expected),
					len(got),
					got,
				)
			}

			for i := range tt.expected {
				if got[i] != tt.expected[i] {
					t.Errorf(
						"queue[%d]: expected %v, got %v",
						i,
						tt.expected[i],
						got[i],
					)
				}
			}
		})
	}
}
