package main

import (
	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/db"
	"github.com/shainilps/relay/internal/keymanager"
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
	db := db.NewClient()
}

