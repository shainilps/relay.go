package config

import (
	"fmt"

	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

func LoadConfig() {

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	viper.AddConfigPath(".")

	err := viper.ReadInConfig()

	if err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	if _, err := ParseNetwork(viper.GetString("app.network")); err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}
}

func ParseNetwork(value string) (model.Network, error) {
	switch network := model.Network(value); network {
	case model.MAIN, model.TEST:
		return network, nil
	default:
		return "", fmt.Errorf("app.network must be MAIN or TEST, got %q", value)
	}
}

func Network() model.Network {
	network, err := ParseNetwork(viper.GetString("app.network"))
	if err != nil {
		panic(err)
	}
	return network
}
