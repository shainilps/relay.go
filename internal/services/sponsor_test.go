package services

import (
	"errors"
	"testing"

	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/shainilps/relay/internal/rabbitmq"
)

func zeroInputTx(t *testing.T, outputs ...uint64) *transaction.Transaction {
	t.Helper()
	_, lockingScript := newKey(t)
	tx := transaction.NewTransaction()
	for _, amount := range outputs {
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: amount, LockingScript: lockingScript})
	}
	return tx
}

func dataTx(t *testing.T) *transaction.Transaction {
	t.Helper()
	data, err := script.NewFromASM("OP_FALSE OP_RETURN 68656c6c6f")
	if err != nil {
		t.Fatal(err)
	}
	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 0, LockingScript: data})
	return tx
}

func TestSponsorAmount(t *testing.T) {
	data := dataTx(t)
	amount, err := sponsorAmount(data, 20000)
	if err != nil || amount != feeForSize(data.Size()) {
		t.Fatalf("expected a data tx to need only its fee, got %d %v", amount, err)
	}

	payment := zeroInputTx(t, 100)
	amount, err = sponsorAmount(payment, 20000)
	if err != nil || amount != 100+feeForSize(payment.Size()) {
		t.Fatalf("expected a zero-input tx to need its outputs plus fee, got %d %v", amount, err)
	}

	funded := signedTx(t, 900, sighash.AllForkID)
	amount, err = sponsorAmount(funded, 20000)
	if err != nil || amount != 0 {
		t.Fatalf("expected a fully funded tx to need nothing, got %d %v", amount, err)
	}

	short := signedTx(t, 995, sighash.All|sighash.AnyOneCanPay|sighash.ForkID)
	amount, err = sponsorAmount(short, 20000)
	if err != nil || amount != 995+feeForSize(short.Size())-1000 {
		t.Fatalf("expected a partly funded tx to need the difference, got %d %v", amount, err)
	}

	_, err = sponsorAmount(zeroInputTx(t, 50000), 20000)
	if !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("expected a request above the limit to be rejected, got %v", err)
	}
}

func TestSponsoredZeroInputTxIsValid(t *testing.T) {
	for name, tx := range map[string]*transaction.Transaction{
		"data":        dataTx(t),
		"payment":     zeroInputTx(t, 100),
		"big payment": zeroInputTx(t, 15000, 3000),
	} {
		t.Run(name, func(t *testing.T) {
			amount, err := sponsorAmount(tx, 20000)
			if err != nil {
				t.Fatal(err)
			}

			feePriv, feeLockingScript := newKey(t)
			for i, queuename := range CalcuateQueues(amount) {
				addInput(t, tx, uint32(i), rabbitmq.QueueToValue[queuename], feePriv, feeLockingScript, sighash.All|sighash.AnyOneCanPay|sighash.ForkID)
			}
			if err := tx.Sign(); err != nil {
				t.Fatal(err)
			}

			if err := verifyScripts(tx, true); err != nil {
				t.Fatalf("expected valid scripts, got %v", err)
			}
			if err := checkFee(tx); err != nil {
				t.Fatalf("expected the chosen queues to cover outputs and fee, got %v", err)
			}
		})
	}
}

func TestParseZeroInputTx(t *testing.T) {
	for name, original := range map[string]*transaction.Transaction{
		"data":    dataTx(t),
		"payment": zeroInputTx(t, 100, 250),
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseTransaction(original.Hex())
			if err != nil {
				t.Fatal(err)
			}
			if parsed.InputCount() != 0 || parsed.OutputCount() != original.OutputCount() || parsed.TotalOutputSatoshis() != original.TotalOutputSatoshis() {
				t.Fatalf("expected the tx to round trip, got %d inputs %d outputs %d sats", parsed.InputCount(), parsed.OutputCount(), parsed.TotalOutputSatoshis())
			}
		})
	}
}
