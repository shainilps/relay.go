package keymanager

import (
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

func TestFeeKeyDerivation(t *testing.T) {
	priv, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	keys := newKeys(priv)
	again := newKeys(priv)

	if keys.GetFeePrivateKey().Wif() != again.GetFeePrivateKey().Wif() {
		t.Fatal("expected the fee key to be the same for the same main key")
	}
	if keys.GetFeePrivateKey().Wif() == priv.Wif() {
		t.Fatal("expected the fee key to differ from the main key")
	}

	fundingAddress, err := keys.GetAddress()
	if err != nil {
		t.Fatal(err)
	}
	feeAddress, err := keys.GetFeeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if fundingAddress.AddressString == feeAddress.AddressString {
		t.Fatal("expected separate funding and fee addresses")
	}

	other, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if newKeys(other).GetFeePrivateKey().Wif() == keys.GetFeePrivateKey().Wif() {
		t.Fatal("expected different main keys to derive different fee keys")
	}
}
