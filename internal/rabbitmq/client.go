package rabbitmq

import (
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/spf13/viper"
)

func NewClient() (*amqp.Connection, *amqp.Channel, error) {

	conn, err := amqp.Dial(viper.GetString("rabbitmq.url"))
	if err != nil {
		return nil, nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, err
	}

	return conn, ch, nil
}

func DeclareQueue(ch *amqp.Channel) (map[QueueName](<-chan amqp.Delivery), error) {

	consumers := make(map[QueueName](<-chan amqp.Delivery))

	for _, queue := range Queues {
		_, err := ch.QueueDeclare(
			string(queue), // name
			true,          // durable
			false,         // delete when unused
			false,         // exclusive
			false,         // no-wait
			nil,           // arguments
		)
		if err != nil {
			return nil, err
		}

		msgs, err := ch.Consume(
			string(queue), // queue
			"",            // consumer
			true,          // auto-ack
			false,         // exclusive
			false,         // no-local
			false,         // no-wait
			nil,           // args
		)
		if err != nil {
			return nil, err
		}

		consumers[queue] = msgs
	}

	return consumers, nil
}

func Publish(ch *amqp.Channel, queueName QueueName, message string) error {

	return ch.Publish(
		"",
		string(queueName),
		false,
		false,
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(message),
		},
	)
}
