package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogAddsTraceIDs(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.Background())
	ctx, span := provider.Tracer("test").Start(context.Background(), "request")

	Log(ctx).Info("inside span")
	Log(context.Background()).Info("outside span")
	span.End()

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}
	inside := entries[0].ContextMap()
	if inside["trace_id"] != span.SpanContext().TraceID().String() || inside["span_id"] != span.SpanContext().SpanID().String() {
		t.Fatalf("expected trace and span ids on the log line, got %v", inside)
	}
	if _, ok := entries[1].ContextMap()["trace_id"]; ok {
		t.Fatal("expected no trace id outside a span")
	}
}

func TestSetupExportsAllSignals(t *testing.T) {
	var mu sync.Mutex
	received := map[string]int{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	viper.Set("telemetry.enabled", true)
	viper.Set("telemetry.otlp_endpoint", collector.URL)
	t.Cleanup(func() {
		viper.Set("telemetry.enabled", false)
		viper.Set("telemetry.otlp_endpoint", "")
	})

	ctx := context.Background()
	shutdown, err := Setup(ctx, model.TEST)
	if err != nil {
		t.Fatal(err)
	}

	spanctx, span := Start(ctx, "test_span")
	Log(spanctx).Info("exported log line")
	Count(spanctx, Metrics.TxSubmitted, attribute.String("endpoint", "test"), attribute.String("result", "stored"))
	span.End()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if received[path] == 0 {
			t.Fatalf("expected an export to %s, got %v", path, received)
		}
	}
}

func TestSetupNeedsEndpointWhenEnabled(t *testing.T) {
	viper.Set("telemetry.enabled", true)
	viper.Set("telemetry.otlp_endpoint", "")
	t.Cleanup(func() { viper.Set("telemetry.enabled", false) })

	if _, err := Setup(context.Background(), model.TEST); err == nil {
		t.Fatal("expected an error without an endpoint")
	}
}
