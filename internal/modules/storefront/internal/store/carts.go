package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrCartFull means a new line would exceed domain.MaxCartLines.
var ErrCartFull = errors.New("storefront store: cart full")

// Carts keeps shoppers' carts in Redis (ADR-0022). A cart is one hash per
// store and token hash, so a token opens nothing in another store and
// Redis never holds a usable token:
//
//	sf:cart:<store>:<hex sha256(token)>
//	  seq        -> counter, the order lines were added in
//	  v:<ulid>   -> "<seq>:<qty>"
//
// Every change renews the key's TTL (domain.CartTTL), so an untouched
// cart expires without a cleanup job.
type Carts struct {
	rdb redis.Cmdable
	put *redis.Script
}

// NewCarts returns the Redis cart store.
func NewCarts(rdb redis.Cmdable) *Carts {
	return &Carts{rdb: rdb, put: redis.NewScript(putLineLua)}
}

// putLineLua sets one line's quantity atomically. A new line gets the
// next sequence number unless the cart is full (-1) or only existing
// lines may change (0). It renews the TTL.
//
// KEYS[1] cart; ARGV: field, qty, max lines, ttl seconds, "1" = existing only.
const putLineLua = `
local cur = redis.call('HGET', KEYS[1], ARGV[1])
local seq
if cur then
  seq = string.match(cur, '^(%d+):')
else
  if ARGV[5] == '1' then return 0 end
  local lines = redis.call('HLEN', KEYS[1]) - 1
  if lines >= tonumber(ARGV[3]) then return -1 end
  seq = redis.call('HINCRBY', KEYS[1], 'seq', 1)
end
redis.call('HSET', KEYS[1], ARGV[1], seq .. ':' .. ARGV[2])
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1
`

func (c *Carts) key(ctx context.Context, tokenHash []byte) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	return "sf:cart:" + store.String() + ":" + hex.EncodeToString(tokenHash), nil
}

func lineField(v ulid.ID) string { return "v:" + v.String() }

// Exists reports whether the cart is live.
func (c *Carts) Exists(ctx context.Context, tokenHash []byte) (bool, error) {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return false, err
	}
	n, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("storefront store: cart exists: %w", err)
	}
	return n == 1, nil
}

// Lines returns the cart's lines in the order they were added; none for
// a missing cart.
func (c *Carts) Lines(ctx context.Context, tokenHash []byte) ([]domain.CartLine, error) {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	all, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("storefront store: cart lines: %w", err)
	}
	type seqLine struct {
		seq  int64
		line domain.CartLine
	}
	lines := make([]seqLine, 0, len(all))
	for field, val := range all {
		id, ok := strings.CutPrefix(field, "v:")
		if !ok {
			continue // the seq counter
		}
		v, err := ulid.Parse(id)
		if err != nil {
			continue // not ours: skip rather than break the shopper's cart
		}
		seq, qty, ok := parseLine(val)
		if !ok {
			continue
		}
		lines = append(lines, seqLine{seq: seq, line: domain.CartLine{VariantID: v, Qty: qty}})
	}
	slices.SortFunc(lines, func(a, b seqLine) int { return int(a.seq - b.seq) })
	out := make([]domain.CartLine, len(lines))
	for i, l := range lines {
		out[i] = l.line
	}
	return out, nil
}

// Qty is one line's quantity (0 when absent).
func (c *Carts) Qty(ctx context.Context, tokenHash []byte, v ulid.ID) (int, error) {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return 0, err
	}
	val, err := c.rdb.HGet(ctx, key, lineField(v)).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("storefront store: cart line: %w", err)
	}
	_, qty, _ := parseLine(val)
	return qty, nil
}

// Put sets a line's quantity (1..MaxLineQty), adding the line if it is
// new and existingOnly is false. A new line in a full cart is
// ErrCartFull; with existingOnly a missing line is domain.ErrNotFound.
func (c *Carts) Put(ctx context.Context, tokenHash []byte, v ulid.ID, qty int, existingOnly bool) error {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return err
	}
	only := "0"
	if existingOnly {
		only = "1"
	}
	res, err := c.put.Run(ctx, c.rdb, []string{key}, lineField(v), qty, domain.MaxCartLines,
		int(domain.CartTTL/time.Second), only).Int()
	switch {
	case err != nil:
		return fmt.Errorf("storefront store: put cart line: %w", err)
	case res == -1:
		return ErrCartFull
	case res == 0:
		return domain.ErrNotFound
	}
	return nil
}

// Remove deletes a line and renews the cart's TTL.
func (c *Carts) Remove(ctx context.Context, tokenHash []byte, v ulid.ID) error {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return err
	}
	_, err = c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HDel(ctx, key, lineField(v))
		p.Expire(ctx, key, domain.CartTTL)
		return nil
	})
	if err != nil {
		return fmt.Errorf("storefront store: remove cart line: %w", err)
	}
	return nil
}

// Units is the number of units in the cart (0 when there is none).
func (c *Carts) Units(ctx context.Context, tokenHash []byte) (int, error) {
	lines, err := c.Lines(ctx, tokenHash)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range lines {
		n += l.Qty
	}
	return n, nil
}

// Delete empties the cart (after its order is placed).
func (c *Carts) Delete(ctx context.Context, tokenHash []byte) error {
	key, err := c.key(ctx, tokenHash)
	if err != nil {
		return err
	}
	if err = c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("storefront store: delete cart: %w", err)
	}
	return nil
}

func parseLine(val string) (seq int64, qty int, ok bool) {
	s, q, found := strings.Cut(val, ":")
	if !found {
		return 0, 0, false
	}
	seq, err1 := strconv.ParseInt(s, 10, 64)
	qty, err2 := strconv.Atoi(q)
	return seq, qty, err1 == nil && err2 == nil && qty > 0 && qty <= domain.MaxLineQty
}
