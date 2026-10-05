package broadcaster

import (
	"context"
	"errors"

	"github.com/shainilps/relay/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type ArcPool struct {
	providers []*Arc
}

func NewArcPool(providers ...*Arc) *ArcPool {
	return &ArcPool{providers: providers}
}

func (p *ArcPool) BroadcastTx(ctx context.Context, txHex string, headers map[string]string) (*BroadcastTxResponse, error) {
	err := errors.New("no arc provider configured")
	for _, provider := range p.providers {
		var response *BroadcastTxResponse
		response, err = provider.BroadcastTx(ctx, txHex, headers)
		recordArcRequest(ctx, provider, "broadcast", err)
		if !IsUnreachable(err) {
			return response, err
		}
		telemetry.Log(ctx).Warn("arc unreachable for broadcast", zap.String("provider", provider.Name()), zap.Error(err))
	}
	return nil, err
}

func (p *ArcPool) GetTxStatus(ctx context.Context, txid string) (*TxStatusResponse, error) {
	err := errors.New("no arc provider configured")
	for _, provider := range p.providers {
		var status *TxStatusResponse
		status, err = provider.GetTxStatus(ctx, txid)
		recordArcRequest(ctx, provider, "status", err)
		if !IsUnreachable(err) {
			return status, err
		}
		telemetry.Log(ctx).Warn("arc unreachable for status", zap.String("provider", provider.Name()), zap.String("txid", txid), zap.Error(err))
	}
	return nil, err
}

func (p *ArcPool) GetPolicy(ctx context.Context) (*PolicyResponse, error) {
	err := errors.New("no arc provider configured")
	for _, provider := range p.providers {
		var policy *PolicyResponse
		policy, err = provider.GetPolicy(ctx)
		recordArcRequest(ctx, provider, "policy", err)
		if !IsUnreachable(err) {
			return policy, err
		}
		telemetry.Log(ctx).Warn("arc unreachable for policy", zap.String("provider", provider.Name()), zap.Error(err))
	}
	return nil, err
}

func arcResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case IsUnreachable(err):
		return "unreachable"
	default:
		return "rejected"
	}
}

func recordArcRequest(ctx context.Context, provider *Arc, operation string, err error) {
	telemetry.Count(ctx, telemetry.Metrics.ArcRequests,
		attribute.String("provider", provider.Name()),
		attribute.String("operation", operation),
		attribute.String("result", arcResult(err)),
	)
}
