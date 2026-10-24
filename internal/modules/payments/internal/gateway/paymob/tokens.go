package paymob

import (
	"crypto/sha256"
	"sync"
	"time"
)

// Auth tokens: an inquiry needs one, asked for with the merchant's API
// key; Paymob's last about an hour. One serves a merchant's inquiries
// for tokenTTL, so an inquiry costs one call, not two.
const (
	tokenTTL  = 50 * time.Minute
	maxTokens = 4096 // merchants with a token at once (GOMEMLIMIT: bounded)
)

// tokenKey keys a token by its region and API key, without keeping the
// key itself.
type tokenKey [sha256.Size]byte

func keyOf(base, apiKey string) tokenKey { return sha256.Sum256([]byte(base + "\x00" + apiKey)) }

type cachedToken struct {
	token string
	until time.Time
}

// tokens is a bounded cache of auth tokens.
type tokens struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[tokenKey]cachedToken
}

func newTokens() *tokens { return &tokens{now: time.Now, m: map[tokenKey]cachedToken{}} }

func (c *tokens) get(k tokenKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.m[k]
	if !ok || !c.now().Before(t.until) {
		delete(c.m, k)
		return "", false
	}
	return t.token, true
}

func (c *tokens) put(k tokenKey, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxTokens {
		now := c.now()
		for key, t := range c.m {
			if !now.Before(t.until) {
				delete(c.m, key)
			}
		}
		if len(c.m) >= maxTokens {
			clear(c.m) // still full of live tokens: start over (each costs one call to get back)
		}
	}
	c.m[k] = cachedToken{token: token, until: c.now().Add(tokenTTL)}
}

func (c *tokens) drop(k tokenKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}
