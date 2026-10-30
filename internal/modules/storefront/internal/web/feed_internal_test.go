package web

import (
	"strings"
	"testing"
)

// FuzzOnlyKey: a feed's query reads as its key alone or not at all.
func FuzzOnlyKey(f *testing.F) {
	for _, s := range []string{"key=abcdefghijklmnopqrstuvwxyz234567", "key=a&x=1", "x=1&key=a", "key=", "", "key=a%26b", "key=a;b", "KEY=a"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		key, ok := onlyKey(raw)
		if !ok {
			return
		}
		if raw != "key="+key || key == "" || strings.ContainsAny(key, "&;=%+") {
			t.Fatalf("onlyKey(%q) = %q", raw, key)
		}
	})
}
