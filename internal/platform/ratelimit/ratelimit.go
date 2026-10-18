// Package ratelimit implements a shared, Redis-backed rate limiter using
// the generic cell rate algorithm (GCRA). One key holds one timestamp, so
// memory per limited subject is constant, and the check-and-update is a
// single atomic Lua script that reads Redis' own clock, so every process
// agrees on time.
//
// Callers decide how to treat a Redis error. Login and OTP endpoints fail
// closed (503): an attacker must not be able to bypass throttling by
// overloading Redis.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limit allows Burst events at once, refilling at Burst per Period. For
// example {Burst: 5, Period: 15*time.Minute} allows 5 immediate attempts,
// then one more every 3 minutes.
type Limit struct {
	Burst  int
	Period time.Duration
}

// Validate reports whether the limit is usable.
func (l Limit) Validate() error {
	if l.Burst < 1 || l.Period < time.Millisecond {
		return fmt.Errorf("ratelimit: invalid limit %+v", l)
	}
	return nil
}

// emission is the time one event "costs".
func (l Limit) emission() time.Duration { return l.Period / time.Duration(l.Burst) }

// Result is the outcome of one check.
type Result struct {
	Allowed    bool
	Remaining  int           // events still allowed right now
	RetryAfter time.Duration // when denied: wait at least this long
}

// Limiter checks limits against Redis.
type Limiter struct {
	rdb    redis.Cmdable
	prefix string
	script *redis.Script
}

// New returns a limiter whose keys are "<prefix>:<key>".
func New(rdb redis.Cmdable, prefix string) (*Limiter, error) {
	if prefix == "" || strings.ContainsAny(prefix, " \t\r\n") {
		return nil, errors.New("ratelimit: prefix must be non-empty with no spaces")
	}
	return &Limiter{rdb: rdb, prefix: prefix, script: redis.NewScript(gcraLua)}, nil
}

// Allow consumes one event for key.
func (l *Limiter) Allow(ctx context.Context, key string, lim Limit) (Result, error) {
	return l.AllowN(ctx, key, lim, 1)
}

// Peek reports whether one event would be allowed, without consuming it.
// Login throttling peeks before checking a password and consumes only on
// failure, so successful logins never lock a user out.
func (l *Limiter) Peek(ctx context.Context, key string, lim Limit) (Result, error) {
	return l.AllowN(ctx, key, lim, 0)
}

// AllowN consumes n events for key (n = 0 peeks). Denied calls consume
// nothing.
func (l *Limiter) AllowN(ctx context.Context, key string, lim Limit, n int) (Result, error) {
	if err := lim.Validate(); err != nil {
		return Result{}, err
	}
	if n < 0 || n > lim.Burst {
		return Result{}, fmt.Errorf("ratelimit: n=%d outside [0, %d]", n, lim.Burst)
	}
	emission := lim.emission().Microseconds()
	res, err := l.script.Run(ctx, l.rdb, []string{l.prefix + ":" + key},
		emission, emission*int64(lim.Burst), n).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	if len(res) != 3 {
		return Result{}, fmt.Errorf("ratelimit: unexpected script result %v", res)
	}
	return Result{
		Allowed:    res[0] == 1,
		Remaining:  int(res[1]),
		RetryAfter: time.Duration(res[2]) * time.Microsecond,
	}, nil
}

// Reset forgets key, e.g. after a successful login clears failure counts.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := l.rdb.Del(ctx, l.prefix+":"+key).Err(); err != nil {
		return fmt.Errorf("ratelimit: reset: %w", err)
	}
	return nil
}

// gcraLua keeps the theoretical arrival time (TAT) in microseconds.
// ARGV: emission interval, burst tolerance (emission * burst), cost.
// Returns {allowed, remaining, retry_after_us}.
const gcraLua = `
local key       = KEYS[1]
local emission  = tonumber(ARGV[1])
local tolerance = tonumber(ARGV[2])
local cost      = tonumber(ARGV[3])

local t   = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

local tat = tonumber(redis.call('GET', key) or now)
if tat < now then tat = now end

-- A peek (cost 0) asks whether one event would be allowed.
local need = cost
if need == 0 then need = 1 end
local allow_at = tat + emission * need - tolerance
if now < allow_at then
  local remaining = math.floor((tolerance - (tat - now)) / emission)
  if remaining < 0 then remaining = 0 end
  return {0, remaining, allow_at - now}
end

if cost > 0 then
  tat = tat + emission * cost
  redis.call('SET', key, tat, 'PX', math.ceil((tat - now) / 1000) + 1)
end
return {1, math.floor((tolerance - (tat - now)) / emission), 0}
`
