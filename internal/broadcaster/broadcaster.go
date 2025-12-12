package broadcaster

import (
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

type Broadcaster struct {
	Arc      *TaalArc
	Explorer *WOCExplorer
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		Arc:      NewTaalArcProvider(model.Network(viper.GetString("app.network")), viper.GetString("arc.token")),
		Explorer: NewWOCExplorerProvider(model.Network(viper.GetString("app.network")), viper.GetString("explorer.token")),
	}

}
