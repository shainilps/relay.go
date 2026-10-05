package keymanager

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	bip39 "github.com/bsv-blockchain/go-sdk/compat/bip39"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	script "github.com/bsv-blockchain/go-sdk/script"
	transaction "github.com/bsv-blockchain/go-sdk/transaction/chaincfg"
	"github.com/shainilps/relay/internal/config"
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const FEE_KEY_INVOICE = "relay fee utxos"

var KeyManager *Keys

type Keys struct {
	privateky *ec.PrivateKey
	feeKey    *ec.PrivateKey
}

func newKeys(privateKey *ec.PrivateKey) *Keys {
	feeKey, err := deriveFeeKey(privateKey)
	if err != nil {
		panic(fmt.Errorf("fee key derivation error: %v", err))
	}
	return &Keys{privateky: privateKey, feeKey: feeKey}
}

func deriveFeeKey(privateKey *ec.PrivateKey) (*ec.PrivateKey, error) {
	return privateKey.DeriveChild(privateKey.PubKey(), FEE_KEY_INVOICE)
}

func addressOf(privateKey *ec.PrivateKey) (*script.Address, error) {
	return script.NewAddressFromPublicKey(privateKey.PubKey(), config.Network() == model.MAIN)
}

func (k *Keys) GetPrivateKey() *ec.PrivateKey {
	return k.privateky
}

func (k *Keys) GetPublicKey() *ec.PublicKey {
	return k.privateky.PubKey()
}

func (k *Keys) GetAddress() (*script.Address, error) {
	return addressOf(k.privateky)
}

func (k *Keys) Addresses() (string, string, error) {
	funding, err := k.GetAddress()
	if err != nil {
		return "", "", err
	}
	fee, err := k.GetFeeAddress()
	if err != nil {
		return "", "", err
	}
	return funding.AddressString, fee.AddressString, nil
}

func (k *Keys) GetFeePrivateKey() *ec.PrivateKey {
	return k.feeKey
}

func (k *Keys) GetFeeAddress() (*script.Address, error) {
	return addressOf(k.feeKey)
}

func Intiate() {
	defer logAddresses()

	{
		privKey, err := readWifFile(".key/wif.txt")
		if err != nil {
			goto menmonic
		}

		zap.L().Info("loaded existing key")
		KeyManager = newKeys(privKey)
		return
	}

menmonic:
	{
		privKey, err := readMnemonicFile(".key/mnemonic.txt")
		if err != nil {
			zap.L().Warn("no usable mnemonic file", zap.Error(err))
			goto generatekey
		}

		zap.L().Info("generated key from mnemonic")
		KeyManager = newKeys(privKey)
		return
	}
generatekey:

	{
		privKey, err := generateNewMasterKey()
		if err != nil {
			panic(fmt.Errorf("new keys error: %v", err))
		}

		zap.L().Info("generated new keys")
		KeyManager = newKeys(privKey)
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

	address, err := script.NewAddressFromPublicKey(privateKey.PubKey(), config.Network() == model.MAIN)
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

	_, feeAddress, err := newKeys(privateKey).Addresses()
	if err != nil {
		return fmt.Errorf("failed to derive fee address: %w", err)
	}

	err = os.WriteFile(".key/fee_address.txt", []byte(feeAddress), 0600)
	if err != nil {
		return fmt.Errorf("failed to save fee address: %w", err)
	}

	return nil
}

func logAddresses() {
	if KeyManager == nil {
		return
	}
	funding, fee, err := KeyManager.Addresses()
	if err != nil {
		zap.L().Error("failed to derive addresses", zap.Error(err))
		return
	}
	zap.L().Info("keys loaded", zap.String("funding_address", funding), zap.String("fee_address", fee))
}

func readWifFile(path string) (*ec.PrivateKey, error) {

	wif, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WIF file: %w", err)
	}

	return ec.PrivateKeyFromWif(strings.TrimSpace(string(wif)))
}

func readMnemonicFile(path string) (*ec.PrivateKey, error) {

	mnemonicBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read WIF file: %w", err)
	}

	masterSeed := bip39.NewSeed(strings.TrimSpace(string(mnemonicBytes)), viper.GetString("key.password"))

	masterKey, err := bip32.NewMaster(masterSeed, &transaction.MainNet)
	if err != nil {
		return nil, err
	}

	return masterKey.ECPrivKey()

}
