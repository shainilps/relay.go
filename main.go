package main

import (
	"log"

	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/db"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/rabbitmq"
)

// it will generate key and maintain it (the address will be one no need to maintain more)
// on startup it will fetch utxo from woc (on every 30seconds it will sync)
// it will fund
// it will boradcast
// it will expose fundandboradcast and broadcast through http and maybe grpc as well (no auth ruqired)

func init() {
	config.LoadConfig()
	keymanager.Intiate()
}

func main() {
	_, err := db.NewClient()
	if err != nil {
		log.Fatalf("failed to create db client: %v", err)
	}

	_, ch, err := rabbitmq.NewClient()
	if err != nil {
		log.Fatalf("failed to create rabbitmq conecton and channel: %v", err)
	}

	_, err = rabbitmq.DeclareQueue(ch)
	if err != nil {
		log.Fatalf("failed to create queues and consumers: %v", err)
	}

}
