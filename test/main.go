package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

func FundAndBroadcast(txHex string) (string, error) {
	url := "http://localhost:8080/fund-and-broadcast"
	data := map[string]string{"txHex": txHex}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var res map[string]interface{}
	err = json.Unmarshal(body, &res)
	if err != nil {
		return "", fmt.Errorf("server error: %s", string(body))
	}
	if txid, ok := res["txid"].(string); ok {
		return txid, nil
	}
	return "", fmt.Errorf("no txid in response")
}

func main() {
	outputSats := 100

	wif := "KxjKoJnKEUA3CcyjLGY7tpixYkc488RWGQ6ykscgBgCzb72UUWmY"

	priv, err := ec.PrivateKeyFromWif(wif)
	if err != nil {
		panic(err)
	}

	address, err := script.NewAddressFromPublicKey(priv.PubKey(), true)
	if err != nil {
		panic(err)
	}

	fmt.Println("address:", address.AddressString)

	tx := transaction.NewTransaction()

	p2pkhLockScript, err := p2pkh.Lock(address)
	if err != nil {
		log.Fatal(err)
	}

	tx.AddOutput(&transaction.TransactionOutput{
		Satoshis:      uint64(outputSats),
		LockingScript: p2pkhLockScript,
	})

	fmt.Println("txhex", tx.Hex())

	id, err := FundAndBroadcast(tx.Hex())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("txid: ", id)
}
