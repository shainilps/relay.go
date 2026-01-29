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
	return script.NewAddressFromPublicKey(k.GetPublicKey(), viper.GetString("app.network") == "main")
}

func Intiate() {
	defer log.Println("keys locked and loaded!!")

	{
		privKey, err := readWifFile(".key/wif.txt")
		if err != nil {
			goto menmonic
		}

		log.Println("loaded existing key")
		KeyManager = &Keys{
			privateky: privKey,
		}
		return
	}

menmonic:
	{
		privKey, err := readMnemonicFile(".key/mnemonic.txt")
		if err != nil {
			log.Println(err)
			goto generatekey
		}

		log.Println("generated key from mnemonic")
		KeyManager = &Keys{
			privateky: privKey,
		}
		return
	}
generatekey:

	{
		privKey, err := generateNewMasterKey()
		if err != nil {
			panic(fmt.Errorf("new keys error: %v", err))
		}

		log.Println("generated new keys")
		KeyManager = &Keys{
			privateky: privKey,
		}
		return
	}
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

	masterSeed := bip39.NewSeed(mnemonic, "")

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

	info, err := os.Stat(".key")
	if err == nil {
		if !info.IsDir() {
			if err := os.Remove(".key"); err != nil {
				return fmt.Errorf("failed to remove .key file: %v", err)
			}
		}
	}

	if err := os.MkdirAll(".key", 0700); err != nil {
		return fmt.Errorf("failed to create .key dir: %v", err)
	}

	err = os.WriteFile(".key/wif.txt", []byte(privateKey.Wif()), 0600)
	if err != nil {
		return fmt.Errorf("failed to save WIF: %w", err)
	}

	err = os.WriteFile(".key/mnemonic.txt", []byte(mnemonic), 0600)
	if err != nil {
		return fmt.Errorf("failed to save mnemonic: %w", err)
	}

	address, err := script.NewAddressFromPublicKey(privateKey.PubKey(), viper.GetString("app.network") == "main")
	if err != nil {
		return fmt.Errorf("failed to save address: %v", err)
	}

	err = os.WriteFile(".key/address.txt", []byte(address.AddressString), 0600)
	if err != nil {
		return fmt.Errorf("failed to save mnemonic: %w", err)
	}

	err = os.WriteFile(".key/pubkey.txt", []byte(hex.EncodeToString(privateKey.PubKey().Compressed())), 0600)
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
