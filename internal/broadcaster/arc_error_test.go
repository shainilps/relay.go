package broadcaster

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsUnreachable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "no error", err: nil, expected: false},
		{name: "network error", err: errors.New("dial tcp: connection refused"), expected: true},
		{name: "timeout", err: context.DeadlineExceeded, expected: true},
		{name: "server error", err: &ArcError{StatusCode: 500}, expected: true},
		{name: "bad gateway", err: &ArcError{StatusCode: 502}, expected: true},
		{name: "rate limited", err: &ArcError{StatusCode: 429}, expected: true},
		{name: "bad token", err: &ArcError{StatusCode: 401}, expected: true},
		{name: "wrapped server error", err: fmt.Errorf("broadcast: %w", &ArcError{StatusCode: 503}), expected: true},
		{name: "malformed tx", err: &ArcError{StatusCode: 461}, expected: false},
		{name: "fee too low", err: &ArcError{StatusCode: 465}, expected: false},
		{name: "bad request", err: &ArcError{StatusCode: 400}, expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsUnreachable(test.err); got != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, got)
			}
		})
	}
}
