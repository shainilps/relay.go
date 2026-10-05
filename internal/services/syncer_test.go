package services

import (
	"testing"

	"github.com/shainilps/relay/internal/model"
)

func TestDecide(t *testing.T) {
	const maxAttempts = 5

	tests := []struct {
		name     string
		attempts int
		mined    bool
		expired  bool
		expected syncAction
	}{
		{name: "never broadcast gets broadcast", attempts: 0, expected: actionBroadcast},
		{name: "mined is synced", attempts: 1, mined: true, expected: actionMarkSynced},
		{name: "not mined is rebroadcast", attempts: 3, expected: actionBroadcast},
		{name: "mined on the last attempt is synced", attempts: maxAttempts, mined: true, expected: actionMarkSynced},
		{name: "not mined after max attempts fails", attempts: maxAttempts, expected: actionMarkFailed},
		{name: "expired is failed even with attempts left", attempts: 1, expired: true, expected: actionMarkExpired},
		{name: "expired without ever reaching arc is failed", attempts: 0, expired: true, expected: actionMarkExpired},
		{name: "mined wins over expired", attempts: 2, mined: true, expired: true, expected: actionMarkSynced},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := decide(&model.Transaction{Attempts: test.attempts}, test.mined, test.expired, maxAttempts)
			if got != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, got)
			}
		})
	}
}
