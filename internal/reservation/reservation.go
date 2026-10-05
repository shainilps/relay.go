package reservation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

const TTL = 2 * time.Minute

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

type Store struct {
	client *redis.Client
}

func NewClient() (*redis.Client, error) {
	url := viper.GetString("redis.url")
	if url == "" {
		return nil, errors.New("redis.url is not set")
	}

	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return client, nil
}

func NewStore(client *redis.Client) *Store {
	return &Store{client: client}
}

func key(outpoint model.Outpoint) string {
	return fmt.Sprintf("relay:fee-utxo:%s:%d", outpoint.TxID, outpoint.Vout)
}

func newToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *Store) acquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	token, err := newToken()
	if err != nil {
		return "", false, err
	}

	ok, err := s.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}

	return token, true, nil
}

func (s *Store) release(ctx context.Context, key string, token string) error {
	if token == "" {
		return nil
	}
	return releaseScript.Run(ctx, s.client, []string{key}, token).Err()
}

func (s *Store) Reserve(ctx context.Context, outpoint model.Outpoint) (string, bool, error) {
	return s.acquire(ctx, key(outpoint), TTL)
}

func (s *Store) Release(ctx context.Context, outpoint model.Outpoint, token string) error {
	return s.release(ctx, key(outpoint), token)
}

func (s *Store) AcquireLock(ctx context.Context, name string, ttl time.Duration) (string, bool, error) {
	return s.acquire(ctx, "relay:lock:"+name, ttl)
}

func (s *Store) ReleaseLock(ctx context.Context, name string, token string) error {
	return s.release(ctx, "relay:lock:"+name, token)
}

func (s *Store) IsReserved(ctx context.Context, outpoint model.Outpoint) (bool, error) {
	count, err := s.client.Exists(ctx, key(outpoint)).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
