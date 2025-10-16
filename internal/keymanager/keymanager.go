package keymanager

import (
	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	bip39 "github.com/bsv-blockchain/go-sdk/compat/bip39"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	script "github.com/bsv-blockchain/go-sdk/script"
	transaction "github.com/bsv-blockchain/go-sdk/transaction/chaincfg"
	"github.com/spf13/viper"
)

// not planning to maintain tone of address to be honest

var KeyManager *Keys

type Keys struct {
	privateky *ec.PrivateKey
}

func (k *Keys) GetPrivateKey() *ec.PrivateKey {
	return k.privateky
}

func (k *Keys) GetPublicKey() *ec.PublicKey {
	return k.privateky.PubKey()
}

func (k *Keys) GetAddress() (*script.Address, error) {
	net := viper.GetString("app.net") == "main"
	return script.NewAddressFromPublicKey(k.GetPublicKey(), net)
}

func Intiate() {
	//look for file wif
	//look for seed

}

func generateNewMasterKey() (*ec.PrivateKey, string, error) {

	seed, err := bip39.NewEntropy(128)
	if err != nil {
		return nil, "", err
	}

	mnemonic, err := bip39.NewMnemonic(seed)
	if err != nil {
		return nil, "", err
	}

	masterSeed := bip39.NewSeed(mnemonic, viper.GetString("key.password"))

	masterKey, err := bip32.NewMaster(masterSeed, &transaction.MainNet)
	if err != nil {
		return nil, "", err
	}

	priv, err := masterKey.ECPrivKey()
	if err != nil {
		return nil, "", err
	}

	return priv, mnemonic, nil
}

func saveWifAndMnemonic(privatekey *ec.PrivateKey, mnemonic string) error {
	.key/wif.txt
	.key/address.txt
	.key/pubkey.txt


	return nil
}

func readWifFile() (*ec.PrivateKey, error) {
	viper.GetString("key.wif_path")

	return nil, nil
}

func readMnemonicFile() (*ec.PrivateKey, error) {

	viper.GetString("key.mnemonic_path")
	return nil, nil
}
