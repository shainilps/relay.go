package reservation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shainilps/relay/internal/model"
)

func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	return NewStore(client), server
}

func TestReserveIsExclusive(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	outpoint := model.Outpoint{TxID: "fund", Vout: 0}

	token, ok, err := store.Reserve(ctx, outpoint)
	if err != nil || !ok || token == "" {
		t.Fatalf("expected first reserve to succeed, got token %q ok %v err %v", token, ok, err)
	}

	_, ok, err = store.Reserve(ctx, outpoint)
	if err != nil || ok {
		t.Fatalf("expected second reserve to fail, got ok %v err %v", ok, err)
	}

	reserved, err := store.IsReserved(ctx, outpoint)
	if err != nil || !reserved {
		t.Fatalf("expected reserved, got %v %v", reserved, err)
	}
}

func TestReleaseOnlyByOwner(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	outpoint := model.Outpoint{TxID: "fund", Vout: 0}

	token, _, err := store.Reserve(ctx, outpoint)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Release(ctx, outpoint, "someone-else"); err != nil {
		t.Fatal(err)
	}
	if reserved, _ := store.IsReserved(ctx, outpoint); !reserved {
		t.Fatal("expected a release with the wrong token to keep the reservation")
	}

	if err := store.Release(ctx, outpoint, token); err != nil {
		t.Fatal(err)
	}
	if reserved, _ := store.IsReserved(ctx, outpoint); reserved {
		t.Fatal("expected the owner to release the reservation")
	}
}

func TestReservationExpires(t *testing.T) {
	ctx := context.Background()
	store, server := newTestStore(t)
	outpoint := model.Outpoint{TxID: "fund", Vout: 0}

	if _, ok, err := store.Reserve(ctx, outpoint); err != nil || !ok {
		t.Fatalf("reserve failed: %v %v", ok, err)
	}

	server.FastForward(TTL)

	if reserved, _ := store.IsReserved(ctx, outpoint); reserved {
		t.Fatal("expected the reservation to expire after the TTL")
	}
	if _, ok, err := store.Reserve(ctx, outpoint); err != nil || !ok {
		t.Fatalf("expected the utxo to be reservable after expiry, got %v %v", ok, err)
	}
}

func TestLockIsExclusiveUntilReleased(t *testing.T) {
	ctx := context.Background()
	store, server := newTestStore(t)

	token, ok, err := store.AcquireLock(ctx, "funding", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected the lock, got %v %v", ok, err)
	}
	if _, ok, _ := store.AcquireLock(ctx, "funding", time.Minute); ok {
		t.Fatal("expected a second acquire to fail")
	}
	if err := store.ReleaseLock(ctx, "funding", token); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.AcquireLock(ctx, "funding", time.Minute); !ok {
		t.Fatal("expected the lock to be free after release")
	}

	server.FastForward(time.Minute)
	if _, ok, _ := store.AcquireLock(ctx, "funding", time.Minute); !ok {
		t.Fatal("expected an abandoned lock to expire")
	}
}
