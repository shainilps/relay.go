package config

import (
	"testing"

	"github.com/shainilps/relay/internal/model"
)

func TestParseNetwork(t *testing.T) {
	for value, expected := range map[string]model.Network{"MAIN": model.MAIN, "TEST": model.TEST} {
		network, err := ParseNetwork(value)
		if err != nil || network != expected {
			t.Fatalf("expected %s, got %s %v", expected, network, err)
		}
	}
	for _, value := range []string{"", "main", "test", "TESTNET", "MAINNET"} {
		if _, err := ParseNetwork(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
