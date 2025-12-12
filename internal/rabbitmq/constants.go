package rabbitmq

type QueueName string

const (
	QUEUE_50  QueueName = "QUEUE_50"
	QUEUE_100 QueueName = "QUEUE_100"
	QUEUE_200 QueueName = "QUEUE_200"
	// QUEUE_400 QueueName = "QUEUE_400"
	// QUEUE_800 QueueName = "QUEUE_800"
	// QUEUE_1600  QueueName = "QUEUE_1600"
	// QUEUE_3200  QueueName = "QUEUE_3200"
	// QUEUE_6400  QueueName = "QUEUE_6400"
	// QUEUE_12800 QueueName = "QUEUE_12800"
)

var ValueToQueue = map[uint64]QueueName{
	50:  QUEUE_50,
	100: QUEUE_100,
	200: QUEUE_200,
	// 400: QUEUE_400,
	// 800: QUEUE_800,
	// 1600:  QUEUE_1600,
	// 3200:  QUEUE_3200,
	// 6400:  QUEUE_6400,
	// 12800: QUEUE_12800,
}

var QueueToValue = map[QueueName]uint64{
	QUEUE_50:  50,
	QUEUE_100: 100,
	QUEUE_200: 200,
	// QUEUE_400: 400,
	// QUEUE_800: 800,
	// QUEUE_1600:  1600,
	// QUEUE_3200:  3200,
	// QUEUE_6400:  6400,
	// QUEUE_12800: 12800,
}

var Queues = []QueueName{
	QUEUE_50,
	QUEUE_100,
	QUEUE_200,
	// QUEUE_400,
	// QUEUE_800,
	// QUEUE_1600,
	// QUEUE_3200,
	// QUEUE_6400,
	// QUEUE_12800,
}
