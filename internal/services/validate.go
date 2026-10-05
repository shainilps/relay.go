package services

import (
	"errors"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/script/interpreter/scriptflag"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

var ErrInvalidTransaction = errors.New("invalid transaction")

var ErrOutOfFee = errors.New("out of fee")

var engine = interpreter.NewEngine()

func hasAllSources(tx *transaction.Transaction) bool {
	for _, input := range tx.Inputs {
		if input.SourceTxOutput() == nil {
			return false
		}
	}
	return true
}

func verifyScripts(tx *transaction.Transaction, requireSources bool) error {
	for i, input := range tx.Inputs {
		prevOutput := input.SourceTxOutput()
		if prevOutput == nil {
			if requireSources {
				return fmt.Errorf("%w: input %d has no source output, send the tx in extended format", ErrInvalidTransaction, i)
			}
			continue
		}

		err := engine.Execute(
			interpreter.WithTx(tx, i, prevOutput),
			interpreter.WithForkID(),
			interpreter.WithAfterGenesis(),
			interpreter.WithFlags(
				scriptflag.EnableSighashForkID|
					scriptflag.UTXOAfterGenesis|
					scriptflag.VerifyDERSignatures|
					scriptflag.VerifyStrictEncoding|
					scriptflag.VerifyNullFail|
					scriptflag.VerifyLowS,
			),
		)
		if err != nil {
			return fmt.Errorf("%w: input %d failed script validation: %v", ErrInvalidTransaction, i, err)
		}
	}

	return nil
}

func checkFee(tx *transaction.Transaction) error {
	inputAmount, err := tx.TotalInputSatoshis()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTransaction, err)
	}

	outputAmount := tx.TotalOutputSatoshis()
	fee := feeForSize(tx.Size())
	if inputAmount < outputAmount+fee {
		return fmt.Errorf("%w: inputs %d sats do not cover outputs %d sats + fee %d sats", ErrInvalidTransaction, inputAmount, outputAmount, fee)
	}

	return nil
}
