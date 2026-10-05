package services

import "testing"

func TestPlanChange(t *testing.T) {
	tests := []struct {
		name         string
		inputAmount  uint64
		outputAmount uint64
		inputCount   int
		outputCount  int
		change       uint64
		fee          uint64
		ok           bool
	}{
		{name: "not enough to pay outputs and fee", inputAmount: 119, outputAmount: 100, inputCount: 1, outputCount: 1, fee: 20, ok: false},
		{name: "exact fee, no change", inputAmount: 120, outputAmount: 100, inputCount: 1, outputCount: 1, fee: 20, ok: true},
		{name: "leftover below min change goes to fee", inputAmount: 137, outputAmount: 100, inputCount: 1, outputCount: 1, fee: 37, ok: true},
		{name: "leftover at min change becomes change", inputAmount: 138, outputAmount: 100, inputCount: 1, outputCount: 1, change: 15, fee: 23, ok: true},
		{name: "large leftover becomes change", inputAmount: 1000, outputAmount: 100, inputCount: 1, outputCount: 1, change: 877, fee: 23, ok: true},
		{name: "small change on a large funding is kept", inputAmount: 1050, outputAmount: 1000, inputCount: 1, outputCount: 1, change: 27, fee: 23, ok: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			change, fee, ok := planChange(test.inputAmount, test.outputAmount, test.inputCount, test.outputCount)
			if change != test.change || fee != test.fee || ok != test.ok {
				t.Fatalf("expected change %d fee %d ok %v, got change %d fee %d ok %v", test.change, test.fee, test.ok, change, fee, ok)
			}
			if ok && test.inputAmount != test.outputAmount+change+fee {
				t.Fatalf("inputs %d do not balance outputs %d + change %d + fee %d", test.inputAmount, test.outputAmount, change, fee)
			}
		})
	}
}

func TestP2pkhTxSizeVarInt(t *testing.T) {
	if got := p2pkhTxSize(1, 252) - p2pkhTxSize(1, 251); got != OUTPUT_SIZE {
		t.Fatalf("expected one output to add %d bytes below 253 outputs, got %d", OUTPUT_SIZE, got)
	}
	if got := p2pkhTxSize(1, 253) - p2pkhTxSize(1, 252); got != OUTPUT_SIZE+2 {
		t.Fatalf("expected the output count varint to grow by 2 bytes at 253 outputs, got %d", got-OUTPUT_SIZE)
	}
}
