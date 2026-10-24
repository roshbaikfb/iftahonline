package paymob

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

func TestTokensExpireAndStayBounded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	c := newTokens()
	c.now = func() time.Time { return now }
	k := keyOf("https://accept.paymob.com", "api-key")
	if bytes.Contains(k[:], []byte("api-key")) {
		t.Fatal("the key keeps the API key")
	}
	c.put(k, "tok")
	if got, ok := c.get(k); !ok || got != "tok" {
		t.Fatalf("get = %q %v", got, ok)
	}
	if _, ok := c.get(keyOf("https://ksa.paymob.com", "api-key")); ok {
		t.Fatal("another region shares the token")
	}
	now = now.Add(tokenTTL)
	if _, ok := c.get(k); ok {
		t.Fatal("an expired token is still served")
	}
	c.drop(k)
	for i := range maxTokens + 10 {
		c.put(keyOf("b", strconv.Itoa(i)), "t")
	}
	if n := len(c.m); n > maxTokens {
		t.Fatalf("%d tokens kept, want at most %d", n, maxTokens)
	}
}
