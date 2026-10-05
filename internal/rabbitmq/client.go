package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/model"
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

	err = ch.Confirm(false)
	if err != nil {
		return nil, nil, err
	}

	return conn, ch, nil
}

func DeclareQueue(ch *amqp.Channel) (map[QueueName](<-chan amqp.Delivery), map[QueueName](amqp.Queue), error) {

	consumers := make(map[QueueName](<-chan amqp.Delivery))
	queues := make(map[QueueName](amqp.Queue))

	for _, queue := range Queues {
		q, err := ch.QueueDeclare(
			string(queue), // name
			true,          // durable
			false,         // delete when unused
			false,         // exclusive
			false,         // no-wait
			nil,           // arguments
		)
		if err != nil {
			return nil, nil, err
		}

		msgs, err := ch.Consume(
			string(queue), // queue
			"",            // consumer
			false,         // auto-ack
			false,         // exclusive
			false,         // no-local
			false,         // no-wait
			nil,           // args
		)
		if err != nil {
			return nil, nil, err
		}

		consumers[queue] = msgs
		queues[queue] = q
	}

	return consumers, queues, nil
}

func Publish(ctx context.Context, ch *amqp.Channel, queueName QueueName, utxo *model.UTXO) error {

	utxoBytes, err := json.Marshal(utxo)
	if err != nil {
		return err
	}

	confirmation, err := ch.PublishWithDeferredConfirmWithContext(
		ctx,
		"",
		string(queueName),
		true,
		false,
		amqp.Publishing{
			ContentType:  "text/json",
			Body:         utxoBytes,
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		return err
	}
	if confirmation == nil {
		return errors.New("channel is not in confirm mode")
	}

	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("broker nacked utxo %s for queue %s", utxo.UtxoID, queueName)
	}

	log.Printf("published utxo %s to queue %s\n", utxo.UtxoID, queueName)
	return nil
}
