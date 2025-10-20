package keymanager

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	bip39 "github.com/bsv-blockchain/go-sdk/compat/bip39"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	script "github.com/bsv-blockchain/go-sdk/script"
	transaction "github.com/bsv-blockchain/go-sdk/transaction/chaincfg"
	"github.com/spf13/viper"
)

// not planning to maintain mutliple address to be honest for now :)

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
	return script.NewAddressFromPublicKey(k.GetPublicKey(), viper.GetString("app.net") == "main")
}

func Intiate() {

	if viper.GetString("key.wif_path") != "" {

		privKey, err := readWifFile(viper.GetString("key.wif_path"))
		if err != nil {
			panic(fmt.Errorf("wif file error: %v", err))
		}

		KeyManager = &Keys{
			privateky: privKey,
		}

	} else if viper.GetString("key.mnemonic_path") != "" {

		privKey, err := readMnemonicFile(viper.GetString("key.mnemonic_path"))
		if err != nil {
			panic(fmt.Errorf("mnemonic file error: %v", err))
		}

		KeyManager = &Keys{
			privateky: privKey,
		}

	} else {

		privKey, err := generateNewMasterKey()
		if err != nil {
			panic(fmt.Errorf("new keys error: %v", err))
		}

		KeyManager = &Keys{
			privateky: privKey,
		}
	}

	log.Println("keys locked and loaded!!")
}

func generateNewMasterKey() (*ec.PrivateKey, error) {

	seed, err := bip39.NewEntropy(128)
	if err != nil {
		return nil, err
	}

	mnemonic, err := bip39.NewMnemonic(seed)
	if err != nil {
		return nil, err
	}

	masterSeed := bip39.NewSeed(mnemonic, viper.GetString("key.password"))

	masterKey, err := bip32.NewMaster(masterSeed, &transaction.MainNet)
	if err != nil {
		return nil, err
	}

	priv, err := masterKey.ECPrivKey()
	if err != nil {
		return nil, err
	}

	err = saveWifAndMnemonic(priv, mnemonic)
	if err != nil {
		return nil, err
	}

	return priv, nil
}

func saveWifAndMnemonic(privateKey *ec.PrivateKey, mnemonic string) error {

	err := os.WriteFile(".key/wif.txt", []byte(privateKey.Wif()), 0600)
	if err != nil {
		return fmt.Errorf("failed to save WIF: %w", err)
	}

	err = os.WriteFile(".key/mnemonic.txt", []byte(mnemonic), 0600)
	if err != nil {
		return fmt.Errorf("failed to save mnemonic: %w", err)
	}

	address, err := script.NewAddressFromPublicKey(privateKey.PubKey(), viper.GetString("app.net") == "main")
	if err != nil {
		return fmt.Errorf("failed to save address: %v", err)
	}

	err = os.WriteFile(".key/address.txt", []byte(address.AddressString), 0600)
	if err != nil {
		return fmt.Errorf("failed to save mnemonic: %w", err)
	}

	err = os.WriteFile(".key/address.txt", []byte(hex.EncodeToString(privateKey.PubKey().Compressed())), 0600)
	if err != nil {
		return fmt.Errorf("failed to save address: %v", err)
	}

	return nil
}

func readWifFile(path string) (*ec.PrivateKey, error) {

	wif, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WIF file: %w", err)
	}

	return ec.PrivateKeyFromWif(string(wif))
}

func readMnemonicFile(path string) (*ec.PrivateKey, error) {

	mnemonicBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WIF file: %w", err)
	}

	masterSeed := bip39.NewSeed(string(mnemonicBytes), viper.GetString("key.password"))

	masterKey, err := bip32.NewMaster(masterSeed, &transaction.MainNet)
	if err != nil {
		return nil, err
	}

	return masterKey.ECPrivKey()

}
