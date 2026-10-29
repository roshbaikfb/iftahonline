package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Added keeps an AddToCart between the cart's form post and the cart
// page it redirects to (ADR-0043): one per cart, for a minute, read once.
// The server makes its event_id, so a shopper cannot name one, and the
// key is the cart's token hash in the store in ctx:
//
//	sf:px-add:<store>:<hex sha256(token)> -> "<event id>.<variant>.<qty>"
type Added struct {
	rdb redis.Cmdable
}

// NewAdded returns the Redis store of pending AddToCarts.
func NewAdded(rdb redis.Cmdable) *Added { return &Added{rdb: rdb} }

func (a *Added) key(ctx context.Context, tokenHash []byte) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	return "sf:px-add:" + store.String() + ":" + hex.EncodeToString(tokenHash), nil
}

// Put keeps value for the cart for ttl, replacing an earlier add.
func (a *Added) Put(ctx context.Context, tokenHash []byte, value string, ttl time.Duration) error {
	key, err := a.key(ctx, tokenHash)
	if err != nil {
		return err
	}
	if err = a.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("storefront store: put added: %w", err)
	}
	return nil
}

// Take returns and forgets the cart's pending add; ok is false without
// one.
func (a *Added) Take(ctx context.Context, tokenHash []byte) (value string, ok bool, err error) {
	key, err := a.key(ctx, tokenHash)
	if err != nil {
		return "", false, err
	}
	value, err = a.rdb.GetDel(ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("storefront store: take added: %w", err)
	}
	return value, true, nil
}
