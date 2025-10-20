package rabbitmq

type QueueName string

const (
	QUEUE_5    QueueName = "QUEUE_5"
	QUEUE_10   QueueName = "QUEUE_10"
	QUEUE_20   QueueName = "QUEUE_20"
	QUEUE_40   QueueName = "QUEUE_40"
	QUEUE_80   QueueName = "QUEUE_80"
	QUEUE_160  QueueName = "QUEUE_160"
	QUEUE_320  QueueName = "QUEUE_320"
	QUEUE_640  QueueName = "QUEUE_640"
	QUEUE_1280 QueueName = "QUEUE_1280"
	QUEUE_2560 QueueName = "QUEUE_2560"
	QUEUE_5120 QueueName = "QUEUE_5120"
)

var ValueToQueue = map[int]QueueName{
	5:    QUEUE_5,
	10:   QUEUE_10,
	20:   QUEUE_20,
	40:   QUEUE_40,
	80:   QUEUE_80,
	160:  QUEUE_160,
	320:  QUEUE_320,
	640:  QUEUE_640,
	1280: QUEUE_1280,
	2560: QUEUE_2560,
	5120: QUEUE_5120,
}

var QueueToValue = map[QueueName]int{
	QUEUE_5:    5,
	QUEUE_10:   10,
	QUEUE_20:   20,
	QUEUE_40:   40,
	QUEUE_80:   80,
	QUEUE_160:  160,
	QUEUE_320:  320,
	QUEUE_640:  640,
	QUEUE_1280: 1280,
	QUEUE_2560: 2560,
	QUEUE_5120: 5120,
}

var Queues = []QueueName{
	QUEUE_5,
	QUEUE_10,
	QUEUE_20,
	QUEUE_40,
	QUEUE_80,
	QUEUE_160,
	QUEUE_320,
	QUEUE_640,
	QUEUE_1280,
	QUEUE_2560,
	QUEUE_5120,
}
