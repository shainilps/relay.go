package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

const (
	CONSUMER_TIMEOUT = 7 * 24 * time.Hour
	DEFAULT_PREFETCH = 50
	RECONNECT_DELAY  = 5 * time.Second
)

type Client struct {
	url      string
	prefetch int

	connMu sync.Mutex
	conn   *amqp.Connection

	publishMu sync.Mutex
	publishCh *amqp.Channel

	deliveries map[QueueName]chan amqp.Delivery
	queues     map[QueueName]amqp.Queue
}

func NewClient() (*Client, error) {

	prefetch := viper.GetInt("rabbitmq.prefetch")
	if prefetch <= 0 {
		prefetch = DEFAULT_PREFETCH
	}

	client := &Client{
		url:        viper.GetString("rabbitmq.url"),
		prefetch:   prefetch,
		deliveries: make(map[QueueName]chan amqp.Delivery),
		queues:     make(map[QueueName]amqp.Queue),
	}

	ch, err := client.channel()
	if err != nil {
		return nil, err
	}
	defer ch.Close()

	for _, queue := range Queues {
		q, err := declareQueue(ch, queue)
		if err != nil {
			return nil, err
		}
		client.queues[queue] = q
		client.deliveries[queue] = make(chan amqp.Delivery)
	}

	return client, nil
}

func declareQueue(ch *amqp.Channel, queue QueueName) (amqp.Queue, error) {
	return ch.QueueDeclare(
		string(queue),
		true,
		false,
		false,
		false,
		amqp.Table{
			"x-queue-type":       "quorum",
			"x-consumer-timeout": CONSUMER_TIMEOUT.Milliseconds(),
			"x-delivery-limit":   int64(-1),
		},
	)
}

func (c *Client) Start(ctx context.Context) {
	for _, queue := range Queues {
		go c.consume(ctx, queue)
	}
}

func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Deliveries(queue QueueName) <-chan amqp.Delivery {
	return c.deliveries[queue]
}

func (c *Client) Queues() map[QueueName]amqp.Queue {
	return c.queues
}

func (c *Client) connection() (*amqp.Connection, error) {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil && !c.conn.IsClosed() {
		return c.conn, nil
	}

	conn, err := amqp.Dial(c.url)
	if err != nil {
		return nil, err
	}
	if c.conn != nil {
		log.Println("reconnected to rabbitmq")
	}
	c.conn = conn
	return conn, nil
}

func (c *Client) channel() (*amqp.Channel, error) {
	conn, err := c.connection()
	if err != nil {
		return nil, err
	}
	return conn.Channel()
}

func (c *Client) consume(ctx context.Context, queue QueueName) {
	for {
		err := c.consumeUntilClosed(ctx, queue)
		if ctx.Err() != nil {
			return
		}

		log.Printf("warning: consumer for queue %s stopped: %v, reconnecting in %s\n", queue, err, RECONNECT_DELAY)
		select {
		case <-ctx.Done():
			return
		case <-time.After(RECONNECT_DELAY):
		}
	}
}

func (c *Client) consumeUntilClosed(ctx context.Context, queue QueueName) error {
	ch, err := c.channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	closed := ch.NotifyClose(make(chan *amqp.Error, 1))

	err = ch.Qos(c.prefetch, 0, false)
	if err != nil {
		return err
	}

	_, err = declareQueue(ch, queue)
	if err != nil {
		return err
	}

	msgs, err := ch.Consume(string(queue), "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	out := c.deliveries[queue]
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case amqpErr := <-closed:
			return fmt.Errorf("channel closed: %v", amqpErr)

		case msg, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			select {
			case out <- msg:
			case <-ctx.Done():
				return ctx.Err()
			case amqpErr := <-closed:
				return fmt.Errorf("channel closed: %v", amqpErr)
			}
		}
	}
}

func (c *Client) publishChannel() (*amqp.Channel, error) {
	if c.publishCh != nil && !c.publishCh.IsClosed() {
		return c.publishCh, nil
	}

	ch, err := c.channel()
	if err != nil {
		return nil, err
	}

	err = ch.Confirm(false)
	if err != nil {
		ch.Close()
		return nil, err
	}

	c.publishCh = ch
	return ch, nil
}

func (c *Client) Publish(ctx context.Context, queueName QueueName, utxo *model.UTXO) error {

	utxoBytes, err := json.Marshal(utxo)
	if err != nil {
		return err
	}

	c.publishMu.Lock()
	defer c.publishMu.Unlock()

	ch, err := c.publishChannel()
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
