package broadcaster

import (
	"context"
	"errors"
	"log"
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
		if !IsUnreachable(err) {
			return response, err
		}
		log.Printf("warning: arc %s unreachable for broadcast: %v\n", provider.Name(), err)
	}
	return nil, err
}

func (p *ArcPool) GetTxStatus(ctx context.Context, txid string) (*TxStatusResponse, error) {
	err := errors.New("no arc provider configured")
	for _, provider := range p.providers {
		var status *TxStatusResponse
		status, err = provider.GetTxStatus(ctx, txid)
		if !IsUnreachable(err) {
			return status, err
		}
		log.Printf("warning: arc %s unreachable for status of %s: %v\n", provider.Name(), txid, err)
	}
	return nil, err
}

func (p *ArcPool) GetPolicy(ctx context.Context) (*PolicyResponse, error) {
	err := errors.New("no arc provider configured")
	for _, provider := range p.providers {
		var policy *PolicyResponse
		policy, err = provider.GetPolicy(ctx)
		if !IsUnreachable(err) {
			return policy, err
		}
		log.Printf("warning: arc %s unreachable for policy: %v\n", provider.Name(), err)
	}
	return nil, err
}
