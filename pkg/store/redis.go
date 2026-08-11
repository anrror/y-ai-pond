package store

import (
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
)

// RedisConn abstracts redigo connection methods.
// Mockable for unit tests (e.g. miniredis implements this via redis.Conn).
type RedisConn interface {
	Do(commandName string, args ...any) (any, error)
	Close() error
	Err() error
}

// RedisStore wraps a redigo connection pool.
type RedisStore struct {
	pool   *redis.Pool
	conn   RedisConn // for unit-test injection (direct conn, bypasses pool)
}

// NewRedis creates a RedisStore backed by a redigo pool.
func NewRedis(addr string) (*RedisStore, error) {
	if addr == "" {
		return nil, fmt.Errorf("store: redis addr is required")
	}
	pool := &redis.Pool{
		MaxIdle:     10,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", addr)
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}
	return &RedisStore{pool: pool}, nil
}

// NewRedisWithConn creates a RedisStore from a pre-built connection (for testing).
func NewRedisWithConn(conn RedisConn) *RedisStore {
	return &RedisStore{conn: conn}
}

func (r *RedisStore) getConn() RedisConn {
	if r.conn != nil {
		return r.conn
	}
	return r.pool.Get()
}

func (r *RedisStore) releaseConn(c RedisConn) {
	if r.conn == nil {
		_ = c.Close()
	}
}

// Ping checks the Redis connection.
func (r *RedisStore) Ping() error {
	conn := r.getConn()
	defer r.releaseConn(conn)
	res, err := redis.String(conn.Do("PING"))
	if err != nil {
		return fmt.Errorf("store: redis ping: %w", err)
	}
	if res != "PONG" {
		return fmt.Errorf("store: unexpected ping response: %s", res)
	}
	return nil
}

// SetShadow stores a device shadow as JSON under the key "shadow:<deviceID>".
func (r *RedisStore) SetShadow(deviceID string, json string) error {
	conn := r.getConn()
	defer r.releaseConn(conn)
	key := fmt.Sprintf("shadow:%s", deviceID)
	_, err := conn.Do("SET", key, json)
	if err != nil {
		return fmt.Errorf("store: set shadow %s: %w", deviceID, err)
	}
	return nil
}

// GetShadow retrieves the device shadow JSON.
func (r *RedisStore) GetShadow(deviceID string) (string, error) {
	conn := r.getConn()
	defer r.releaseConn(conn)
	key := fmt.Sprintf("shadow:%s", deviceID)
	res, err := redis.String(conn.Do("GET", key))
	if err != nil {
		if err == redis.ErrNil {
			return "", fmt.Errorf("store: get shadow %s: %w", deviceID, ErrNotFound)
		}
		return "", fmt.Errorf("store: get shadow %s: %w", deviceID, err)
	}
	return res, nil
}

// SetNX sets a key with TTL only if it does not exist (used for alert dedup).
// Returns true if the key was set, false if it already existed.
func (r *RedisStore) SetNX(key string, ttlSec int) (bool, error) {
	conn := r.getConn()
	defer r.releaseConn(conn)
	res, err := redis.String(conn.Do("SET", key, "1", "NX", "EX", ttlSec))
	if err != nil {
		if err == redis.ErrNil {
			return false, nil
		}
		return false, fmt.Errorf("store: setnx %s: %w", key, err)
	}
	return res == "OK", nil
}

// Close releases the Redis connection pool.
func (r *RedisStore) Close() error {
	if r.pool != nil {
		return r.pool.Close()
	}
	return nil
}
