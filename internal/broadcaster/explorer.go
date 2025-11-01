package broadcaster

import (
	"context"

	"github.com/shainilps/relay/internal/model"
)

type WOCExplorer struct {
	network model.Network
	token   string
}

func NewWOCExplorerProvider(network model.Network, token string) *WOCExplorer {
	return &WOCExplorer{
		network,
		token,
	}
}

func (w *WOCExplorer) GetUtxosForAddress(ctx context.Context, address *string) {}
