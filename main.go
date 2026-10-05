package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/shainilps/relay/internal/telemetry"
	"github.com/spf13/viper"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

func init() {
	config.LoadConfig()
}

func main() {

	network := config.Network()

	shutdownTelemetry, err := telemetry.Setup(context.Background(), network)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to set up telemetry:", err)
		os.Exit(1)
	}

	zap.L().Info("starting relay", zap.String("network", string(network)))

	keymanager.Intiate()

	authenticator, err := auth.Load()
	if err != nil {
		zap.L().Fatal("invalid auth config", zap.Error(err))
	}

	db, err := db.NewClient(network)
	if err != nil {
		zap.L().Fatal("failed to create db client", zap.Error(err))
	}

	mq, err := rabbitmq.NewClient(network)
	if err != nil {
		zap.L().Fatal("failed to connect to rabbitmq and declare queues", zap.Error(err))
	}

	redisClient, err := reservation.NewClient()
	if err != nil {
		zap.L().Fatal("failed to connect to redis", zap.Error(err))
	}

	bd := broadcaster.NewBroadcaster()

	service := services.NewRelayService(db, bd, mq, reservation.NewStore(redisClient, network))
	if err := service.RegisterMetrics(); err != nil {
		zap.L().Fatal("failed to register metrics", zap.Error(err))
	}

	appctx, cancel := context.WithCancel(context.Background())
	mq.Start(appctx)
	go service.StartEngine(appctx)
	go service.StartSyncer(appctx)

	handler := handlers.NewHandler(service)

	router := otelhttp.NewHandler(
		handlers.NewRouter(handler, authenticator.Middleware),
		"relay",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Path }),
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/health" }),
	)

	server := http.Server{
		Addr:    viper.GetString("app.addr"),
		Handler: router,
	}

	sigchan := make(chan os.Signal, 1)
	serverClose := make(chan struct{})
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		zap.L().Info("server started", zap.String("addr", viper.GetString("app.addr")))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			zap.L().Error("server shutdown error", zap.Error(err))
		}

		zap.L().Info("server shutdown gracefully")

		cancel()
		serverClose <- struct{}{}
	}()

	go func() {
		for {
			<-sigchan
			zap.L().Info("received signal to shutdown")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := server.Shutdown(ctx)
			cancel()
			if err == nil {
				return
			}

			zap.L().Error("shutdown failed", zap.Error(err))
			zap.L().Info("waiting for another signal")
		}
	}()

	<-serverClose

	if err := mq.Close(); err != nil {
		zap.L().Error("failed to close rabbitmq connection", zap.Error(err))
	}

	if err := redisClient.Close(); err != nil {
		zap.L().Error("failed to close redis connection", zap.Error(err))
	}

	telemetryctx, telemetryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer telemetryCancel()
	if err := shutdownTelemetry(telemetryctx); err != nil {
		fmt.Fprintln(os.Stderr, "failed to flush telemetry:", err)
	}
}
