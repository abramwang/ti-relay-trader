package credentials

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrSecretNotFound = errors.New("credential secret is not configured")

type SecretStore interface {
	Get(ctx context.Context, key string) (string, error)
	SetIfAbsent(ctx context.Context, key, value string) (bool, error)
	AcquireLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	SetIfLockOwner(ctx context.Context, lockKey, token, key, value string) (bool, error)
	DeleteIfLockOwner(ctx context.Context, lockKey, token, key string) (bool, error)
	ReleaseLock(ctx context.Context, key, token string) error
	Close() error
}

type RedisSecretStore struct {
	client *redis.Client
}

func OpenRedisSecretStore(redisURL string) (*RedisSecretStore, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, errors.New("parse Redis credential store URL")
	}
	return &RedisSecretStore{client: redis.NewClient(options)}, nil
}

func (store *RedisSecretStore) Get(ctx context.Context, key string) (string, error) {
	value, err := store.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", errors.New("read Redis credential value")
	}
	return value, nil
}

func (store *RedisSecretStore) SetIfAbsent(ctx context.Context, key, value string) (bool, error) {
	created, err := store.client.SetNX(ctx, key, value, 0).Result()
	if err != nil {
		return false, errors.New("write Redis credential version")
	}
	return created, nil
}

func (store *RedisSecretStore) AcquireLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	acquired, err := store.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, errors.New("acquire Redis credential lock")
	}
	return acquired, nil
}

const setIfLockOwnerLua = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[2], ARGV[2])
return 1
`

func (store *RedisSecretStore) SetIfLockOwner(ctx context.Context, lockKey, token, key, value string) (bool, error) {
	result, err := store.client.Eval(ctx, setIfLockOwnerLua, []string{lockKey, key}, token, value).Int64()
	if err != nil {
		return false, errors.New("activate Redis credential version")
	}
	return result == 1, nil
}

const deleteIfLockOwnerLua = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('DEL', KEYS[2])
return 1
`

func (store *RedisSecretStore) DeleteIfLockOwner(ctx context.Context, lockKey, token, key string) (bool, error) {
	result, err := store.client.Eval(ctx, deleteIfLockOwnerLua, []string{lockKey, key}, token).Int64()
	if err != nil {
		return false, errors.New("disable Redis credential pointer")
	}
	return result == 1, nil
}

const releaseLockLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

func (store *RedisSecretStore) ReleaseLock(ctx context.Context, key, token string) error {
	if err := store.client.Eval(ctx, releaseLockLua, []string{key}, token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return errors.New("release Redis credential lock")
	}
	return nil
}

func (store *RedisSecretStore) Close() error {
	if store == nil || store.client == nil {
		return nil
	}
	return store.client.Close()
}
