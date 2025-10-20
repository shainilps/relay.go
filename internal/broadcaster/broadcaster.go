package broadcaster

import (
	"github.com/spf13/viper"
)

type Broadcaster struct {
	WOCToken  string
	TaalToken string
}

func NewBoradcaster() *Broadcaster {
	return &Broadcaster{
		TaalToken: viper.GetString(""),
		WOCToken:  viper.GetString(""),
	}
}

func (b *Broadcaster) Broadcast(txhex string) {

}

func (b *Broadcaster) GetFee(txhex string) {

}

func (b *Broadcaster) GetUtxo(address string) {

}
