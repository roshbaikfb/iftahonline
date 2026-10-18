package secretbox_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

func newKey(t testing.TB) string {
	t.Helper()
	b := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestParseKeyring(t *testing.T) {
	t.Parallel()
	k1, k2 := newKey(t), newKey(t)
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	tests := []struct {
		name string
		spec string
		ok   bool
	}{
		{"one key", "1:" + k1, true},
		{"two keys with spaces", " 2:" + k2 + " , 1:" + k1 + " ", true},
		{"url-safe raw base64", "1:" + strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(k1), "="), true},
		{"empty", "", false},
		{"no version", k1, false},
		{"version zero", "0:" + k1, false},
		{"version too big", "256:" + k1, false},
		{"not base64", "1:!!!", false},
		{"short key", "1:" + short, false},
		{"duplicate version", "1:" + k1 + ",1:" + k2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := secretbox.ParseKeyring(tt.spec)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, ok = %v", err, tt.ok)
			}
		})
	}
}

func TestSealOpen(t *testing.T) {
	t.Parallel()
	k, err := secretbox.ParseKeyring("1:" + newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("totp:user-1")
	sealed, err := k.Seal([]byte("JBSWY3DPEHPK3PXP"), aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Open(sealed, aad)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open = %q, %v", got, err)
	}

	again, _ := k.Seal([]byte("JBSWY3DPEHPK3PXP"), aad)
	if bytes.Equal(sealed, again) {
		t.Fatal("nonces must differ between seals")
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	unknownVersion := bytes.Clone(sealed)
	unknownVersion[0] = 9
	for name, tc := range map[string]struct {
		sealed []byte
		aad    []byte
	}{
		"wrong aad":       {sealed, []byte("totp:user-2")},
		"tampered":        {tampered, aad},
		"unknown version": {unknownVersion, aad},
		"too short":       {sealed[:10], aad},
		"empty":           {nil, aad},
	} {
		if _, err := k.Open(tc.sealed, tc.aad); !errors.Is(err, secretbox.ErrOpen) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestRotation(t *testing.T) {
	t.Parallel()
	k1, k2 := newKey(t), newKey(t)
	old, err := secretbox.ParseKeyring("1:" + k1)
	if err != nil {
		t.Fatal(err)
	}
	sealedOld, _ := old.Seal([]byte("secret"), nil)

	rotated, err := secretbox.ParseKeyring("2:" + k2 + ",1:" + k1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Open(sealedOld, nil); err != nil || string(got) != "secret" {
		t.Fatalf("old data must open after rotation: %q, %v", got, err)
	}
	if !rotated.NeedsRotation(sealedOld) {
		t.Fatal("old data must need rotation")
	}
	sealedNew, _ := rotated.Seal([]byte("secret"), nil)
	if rotated.NeedsRotation(sealedNew) || sealedNew[0] != 2 {
		t.Fatal("new data must use the current key")
	}
	if _, err := old.Open(sealedNew, nil); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatal("old keyring must not open data sealed with a newer key")
	}
	if !rotated.NeedsRotation(nil) {
		t.Fatal("empty input needs (re)sealing")
	}
}

func TestStringHidesKeys(t *testing.T) {
	t.Parallel()
	key := newKey(t)
	k, _ := secretbox.ParseKeyring("1:" + key)
	for _, s := range []string{k.String(), k.LogValue().String()} {
		if strings.Contains(s, key) || !strings.Contains(s, "keys: 1") {
			t.Fatalf("unexpected rendering %q", s)
		}
	}
}

func FuzzParseKeyring(f *testing.F) {
	f.Add("1:" + newKey(f))
	f.Add("2:abc,1:def")
	f.Add(":")
	f.Fuzz(func(t *testing.T, spec string) {
		k, err := secretbox.ParseKeyring(spec)
		if err != nil {
			return
		}
		sealed, err := k.Seal([]byte("x"), []byte("aad"))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := k.Open(sealed, []byte("aad")); err != nil || string(got) != "x" {
			t.Fatalf("round trip: %q, %v", got, err)
		}
	})
}

func FuzzOpen(f *testing.F) {
	k, err := secretbox.ParseKeyring("1:" + newKey(f))
	if err != nil {
		f.Fatal(err)
	}
	valid, _ := k.Seal([]byte("x"), nil)
	f.Add(valid)
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, b []byte) {
		// Only the one genuine ciphertext may open; anything else opening
		// means authentication is broken.
		if _, err := k.Open(b, nil); err == nil && !bytes.Equal(b, valid) {
			t.Fatalf("forged ciphertext accepted: %x", b)
		}
	})
}
