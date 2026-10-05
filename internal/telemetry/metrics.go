package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const INSTRUMENTATION_NAME = "github.com/shainilps/relay"

func Tracer() trace.Tracer {
	return otel.Tracer(INSTRUMENTATION_NAME)
}

func Meter() metric.Meter {
	return otel.Meter(INSTRUMENTATION_NAME)
}

type Instruments struct {
	ArcRequests       metric.Int64Counter
	TxSubmitted       metric.Int64Counter
	TxSettled         metric.Int64Counter
	TxTimeToMined     metric.Float64Histogram
	FeeUtxoEvents     metric.Int64Counter
	FundingRounds     metric.Int64Counter
	FundingOutputs    metric.Int64Counter
	UtxoRecovery      metric.Int64Counter
	AuthRejected      metric.Int64Counter
	RabbitReconnected metric.Int64Counter
}

var Metrics = newInstruments()

func newInstruments() Instruments {
	meter := Meter()
	counter := func(name string, description string) metric.Int64Counter {
		instrument, err := meter.Int64Counter(name, metric.WithDescription(description))
		if err != nil {
			zap.L().Error("failed to create counter", zap.String("name", name), zap.Error(err))
		}
		return instrument
	}

	timeToMined, err := meter.Float64Histogram("relay.tx.time_to_mined",
		metric.WithDescription("Seconds from storing a tx to arc reporting it mined"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(60, 300, 600, 1200, 1800, 3600, 7200, 21600, 86400),
	)
	if err != nil {
		zap.L().Error("failed to create histogram", zap.String("name", "relay.tx.time_to_mined"), zap.Error(err))
	}

	return Instruments{
		ArcRequests:       counter("relay.arc.requests", "Requests to arc by provider, operation and result"),
		TxSubmitted:       counter("relay.tx.submitted", "Txs submitted to the relay by endpoint and result"),
		TxSettled:         counter("relay.tx.settled", "Txs reaching a final state by status and reason"),
		TxTimeToMined:     timeToMined,
		FeeUtxoEvents:     counter("relay.fee_utxo.events", "Fee utxo lifecycle events by queue and event"),
		FundingRounds:     counter("relay.funding.rounds", "Funding rounds by result"),
		FundingOutputs:    counter("relay.funding.outputs", "Fee utxos created by queue"),
		UtxoRecovery:      counter("relay.utxo.recovery", "Utxos checked for recovery by kind and result"),
		AuthRejected:      counter("relay.auth.rejected", "Requests rejected for missing or wrong credentials"),
		RabbitReconnected: counter("relay.rabbitmq.reconnects", "Rabbitmq consumer reconnects by queue"),
	}
}

func Count(ctx context.Context, counter metric.Int64Counter, attrs ...attribute.KeyValue) {
	if counter == nil {
		return
	}
	counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func CountN(ctx context.Context, counter metric.Int64Counter, n int64, attrs ...attribute.KeyValue) {
	if counter == nil || n == 0 {
		return
	}
	counter.Add(ctx, n, metric.WithAttributes(attrs...))
}

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
