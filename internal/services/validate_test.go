package services

import (
	"errors"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

const fakeSourceTxID = "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b"

func newKey(t *testing.T) (*ec.PrivateKey, *script.Script) {
	t.Helper()
	priv, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	address, err := script.NewAddressFromPublicKey(priv.PubKey(), true)
	if err != nil {
		t.Fatal(err)
	}
	lockingScript, err := p2pkh.Lock(address)
	if err != nil {
		t.Fatal(err)
	}
	return priv, lockingScript
}

func addInput(t *testing.T, tx *transaction.Transaction, vout uint32, amount uint64, priv *ec.PrivateKey, lockingScript *script.Script, sgh sighash.Flag) {
	t.Helper()
	unlocker, err := p2pkh.Unlock(priv, &sgh)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.AddInputFrom(fakeSourceTxID, vout, lockingScript.String(), amount, unlocker); err != nil {
		t.Fatal(err)
	}
}

func signedTx(t *testing.T, outputAmount uint64, sgh sighash.Flag) *transaction.Transaction {
	t.Helper()
	priv, lockingScript := newKey(t)
	tx := transaction.NewTransaction()
	addInput(t, tx, 0, 1000, priv, lockingScript, sgh)
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: outputAmount, LockingScript: lockingScript})
	if err := tx.Sign(); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestVerifyScripts(t *testing.T) {
	t.Run("valid signed tx passes", func(t *testing.T) {
		tx := signedTx(t, 900, sighash.AllForkID)
		if err := verifyScripts(tx, true); err != nil {
			t.Fatalf("expected valid tx, got %v", err)
		}
	})

	t.Run("tampered output fails", func(t *testing.T) {
		tx := signedTx(t, 900, sighash.AllForkID)
		tx.Outputs[0].Satoshis = 800
		if err := verifyScripts(tx, true); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("expected ErrInvalidTransaction, got %v", err)
		}
	})

	t.Run("extended format keeps the source outputs", func(t *testing.T) {
		efHex, err := signedTx(t, 900, sighash.AllForkID).EFHex()
		if err != nil {
			t.Fatal(err)
		}
		tx, err := parseTransaction(efHex)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyScripts(tx, true); err != nil {
			t.Fatalf("expected valid tx, got %v", err)
		}
	})

	t.Run("raw tx has no sources", func(t *testing.T) {
		tx, err := parseTransaction(signedTx(t, 900, sighash.AllForkID).Hex())
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyScripts(tx, true); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("expected ErrInvalidTransaction when sources are required, got %v", err)
		}
		if err := verifyScripts(tx, false); err != nil {
			t.Fatalf("expected inputs without sources to be skipped, got %v", err)
		}
	})

	addFeeInput := func(t *testing.T, tx *transaction.Transaction) {
		t.Helper()
		feePriv, feeLockingScript := newKey(t)
		addInput(t, tx, 1, 50, feePriv, feeLockingScript, sighash.All|sighash.AnyOneCanPay|sighash.ForkID)
		if err := tx.SignUnsigned(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("fee input keeps an anyonecanpay client signature valid", func(t *testing.T) {
		tx := signedTx(t, 900, sighash.All|sighash.AnyOneCanPay|sighash.ForkID)
		addFeeInput(t, tx)
		if err := verifyScripts(tx, true); err != nil {
			t.Fatalf("expected valid tx, got %v", err)
		}
	})

	t.Run("fee input breaks a sighash all client signature", func(t *testing.T) {
		tx := signedTx(t, 900, sighash.AllForkID)
		addFeeInput(t, tx)
		if err := verifyScripts(tx, true); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("expected ErrInvalidTransaction, got %v", err)
		}
	})
}

func TestCheckFee(t *testing.T) {
	if err := checkFee(signedTx(t, 900, sighash.AllForkID)); err != nil {
		t.Fatalf("expected fee to be covered, got %v", err)
	}
	if err := checkFee(signedTx(t, 1000, sighash.AllForkID)); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("expected ErrInvalidTransaction for a tx with no fee, got %v", err)
	}
}
