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

	"github.com/shainilps/relay/internal/auth"
	"github.com/shainilps/relay/internal/broadcaster"
	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/db"
	"github.com/shainilps/relay/internal/handlers"
	"github.com/shainilps/relay/internal/keymanager"
	"github.com/shainilps/relay/internal/rabbitmq"
	"github.com/shainilps/relay/internal/reservation"
	"github.com/shainilps/relay/internal/services"
	"github.com/spf13/viper"
)

func init() {
	config.LoadConfig()
	keymanager.Intiate()
}

func main() {

	authenticator, err := auth.Load()
	if err != nil {
		log.Fatalf("invalid auth config: %v", err)
	}

	network := config.Network()
	log.Printf("running on %s network\n", network)

	db, err := db.NewClient(network)
	if err != nil {
		log.Fatalf("failed to create db client: %v", err)
	}

	mq, err := rabbitmq.NewClient(network)
	if err != nil {
		log.Fatalf("failed to connect to rabbitmq and declare queues: %v", err)
	}

	redisClient, err := reservation.NewClient()
	if err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}

	bd := broadcaster.NewBroadcaster()

	service := services.NewRelayService(db, bd, mq, reservation.NewStore(redisClient, network))

	appctx, cancel := context.WithCancel(context.Background())
	mq.Start(appctx)
	go service.StartEngine(appctx)
	go service.StartSyncer(appctx)

	handler := handlers.NewHandler(service)

	router := handlers.NewRouter(handler, authenticator.Middleware)

	server := http.Server{
		Addr:    viper.GetString("app.addr"),
		Handler: router,
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

	if err := mq.Close(); err != nil {
		log.Println("failed to close rabbitmq connection:", err)
	}

	if err := redisClient.Close(); err != nil {
		log.Println("failed to close redis connection:", err)
	}

}
