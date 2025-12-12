package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/db"
	"github.com/shainilps/relay/internal/handlers"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/shainilps/relay/internal/services"
	"github.com/spf13/viper"
)

func init() {
	config.LoadConfig()
	keymanager.Intiate()
}

func main() {
	db, err := db.NewClient()
	if err != nil {
		log.Fatalf("failed to create db client: %v", err)
	}

	_, ch, err := rabbitmq.NewClient()
	if err != nil {
		log.Fatalf("failed to create rabbitmq conecton and channel: %v", err)
	}

	consumers, queues, err := rabbitmq.DeclareQueue(ch)
	if err != nil {
		log.Fatalf("failed to create queues and consumers: %v", err)
	}

	appctx, cancel := context.WithCancel(context.Background())

	broadcaster := broadcaster.NewBroadcaster()
	// 13Ny8SNCEHrTunpn7ZzGqMS4RAPoXvJXnx

	fundingChan := make(chan rabbitmq.QueueName)

	service := services.NewRelayService(db, ch, broadcaster, consumers, queues, fundingChan)

	go service.StartEngine(appctx)
	go service.StartQueueMonitor(appctx)

	handler := handlers.NewHandler(service)

	mux := http.NewServeMux()

	mux.HandleFunc("/broadcast", handler.Broadcast)
	mux.HandleFunc("/fund-and-broadcast", handler.FundAndBroadcast)
	mux.HandleFunc("/funding-address", handler.GetFundingAddress)

	server := http.Server{
		Addr:    viper.GetString("app.addr"),
		Handler: mux,
	}

	sigchan := make(chan os.Signal, 1)
	serverClose := make(chan struct{})
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Println("server started at:", viper.GetString("app.addr"))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Println("server shutdown error:", err)
		}

		log.Println("server shutdown gracefully")

		cancel()
		close(fundingChan)
		serverClose <- struct{}{}
	}()

	go func() {
		for {
			<-sigchan
			log.Println("received signal to shutdown")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := server.Shutdown(ctx)
			cancel()
			if err == nil {
				return
			}

			log.Println("shutdown failed:", err)
			log.Println("waiting for another signal…")
		}
	}()

	<-serverClose

}
