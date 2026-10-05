package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	SERVICE_NAME            = "relay"
	DEFAULT_METRIC_INTERVAL = 15 * time.Second
)

type Shutdown func(ctx context.Context) error

func logLevel() zapcore.Level {
	level, err := zapcore.ParseLevel(viper.GetString("log.level"))
	if err != nil {
		return zapcore.InfoLevel
	}
	return level
}

func stdoutCore(level zapcore.Level) zapcore.Core {
	encoder := zap.NewProductionEncoderConfig()
	encoder.TimeKey = "time"
	encoder.EncodeTime = zapcore.ISO8601TimeEncoder
	return zapcore.NewCore(zapcore.NewJSONEncoder(encoder), zapcore.Lock(os.Stdout), level)
}

func Setup(ctx context.Context, network model.Network) (Shutdown, error) {
	level := logLevel()
	stdout := stdoutCore(level)

	if !viper.GetBool("telemetry.enabled") {
		zap.ReplaceGlobals(zap.New(stdout))
		return func(context.Context) error {
			_ = zap.L().Sync()
			return nil
		}, nil
	}

	endpoint := strings.TrimRight(viper.GetString("telemetry.otlp_endpoint"), "/")
	if endpoint == "" {
		return nil, errors.New("telemetry.enabled is true but telemetry.otlp_endpoint is not set")
	}

	hostname, _ := os.Hostname()
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", SERVICE_NAME),
		attribute.String("service.instance.id", hostname),
		attribute.String("deployment.environment.name", strings.ToLower(string(network))),
	))
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"))
	if err != nil {
		return nil, err
	}
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(res))

	metricExporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"))
	if err != nil {
		return nil, err
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(DEFAULT_METRIC_INTERVAL))),
		sdkmetric.WithResource(res),
	)

	logExporter, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(endpoint+"/v1/logs"))
	if err != nil {
		return nil, err
	}
	loggerProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)), sdklog.WithResource(res))

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	global.SetLoggerProvider(loggerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	exported, err := zapcore.NewIncreaseLevelCore(otelzap.NewCore(SERVICE_NAME, otelzap.WithLoggerProvider(loggerProvider)), level)
	if err != nil {
		return nil, err
	}
	zap.ReplaceGlobals(zap.New(zapcore.NewTee(stdout, exported)))

	return func(ctx context.Context) error {
		_ = zap.L().Sync()
		return errors.Join(
			tracerProvider.Shutdown(ctx),
			meterProvider.Shutdown(ctx),
			loggerProvider.Shutdown(ctx),
		)
	}, nil
}

func Log(ctx context.Context) *zap.Logger {
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return zap.L()
	}
	return zap.L().With(
		zap.String("trace_id", span.TraceID().String()),
		zap.String("span_id", span.SpanID().String()),
		zap.Field{Key: "otel_context", Type: zapcore.SkipType, Interface: ctx},
	)
}
