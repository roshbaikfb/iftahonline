package envelope_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
)

func key(t testing.TB) string {
	t.Helper()
	b := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func keyring(t testing.TB, spec string) *secretbox.Keyring {
	t.Helper()
	k, err := secretbox.ParseKeyring(spec)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sealer(t testing.TB, current envelope.KEK, older ...envelope.KEK) *envelope.Sealer {
	t.Helper()
	s, err := envelope.New(current, older...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func opens(t *testing.T, s *envelope.Sealer, blob []byte, aad, want string) {
	t.Helper()
	got, err := s.Open(context.Background(), blob, []byte(aad))
	if err != nil || string(got) != want {
		t.Fatalf("Open = %q, %v; want %q", got, err, want)
	}
}

// otherKEK is a second key holder, as a KMS would be.
type otherKEK struct{ envelope.KeyringKEK }

func (otherKEK) ID() string { return "kms:test" }

// downKEK is a key holder that cannot be reached.
type downKEK struct{ envelope.KeyringKEK }

var errDown = errors.New("kms unreachable")

func (downKEK) Unwrap(context.Context, []byte, []byte) ([]byte, error) { return nil, errDown }

func TestSealOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sealer(t, envelope.FromKeyring(keyring(t, "1:"+key(t))))
	secret := []byte(`{"api_key":"bosta-key","webhook_secret":"w"}`)
	aad := []byte("shipping:bosta:store-a:conn-1")

	blob, err := s.Seal(ctx, secret, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, secret) || bytes.Contains(blob, []byte("bosta-key")) {
		t.Fatal("blob contains the plaintext")
	}
	opens(t, s, blob, string(aad), string(secret))
	again, err := s.Seal(ctx, secret, aad)
	if err != nil || bytes.Equal(again, blob) {
		t.Fatal("two seals of one secret must differ (fresh DEK and nonce)")
	}

	tamper := func(i int) []byte { b := bytes.Clone(blob); b[i] ^= 1; return b }
	refused := map[string]struct {
		blob []byte
		aad  []byte
	}{
		"other AAD (copied to another row)": {blob, []byte("shipping:bosta:store-b:conn-1")},
		"empty":                             {nil, aad},
		"truncated":                         {blob[:len(blob)-1], aad},
		"header only":                       {blob[:8], aad},
		"bad magic":                         {tamper(0), aad},
		"bad version":                       {tamper(1), aad},
		"unknown KEK id":                    {tamper(3), aad},
		"wrapped DEK flipped":               {tamper(10), aad},
		"ciphertext flipped":                {tamper(len(blob) - 1), aad},
	}
	for name, tt := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := s.Open(ctx, tt.blob, tt.aad); !errors.Is(err, envelope.ErrOpen) {
				t.Fatalf("err = %v, want ErrOpen", err)
			}
		})
	}
}

func TestSealLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sealer(t, envelope.FromKeyring(keyring(t, "1:"+key(t))))
	if _, err := s.Seal(ctx, make([]byte, envelope.MaxPlaintext+1), nil); !errors.Is(err, envelope.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	blob, err := s.Seal(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	opens(t, s, blob, "", "")
}

func TestNew(t *testing.T) {
	t.Parallel()
	env := envelope.FromKeyring(keyring(t, "1:"+key(t)))
	if _, err := envelope.New(nil); err == nil {
		t.Fatal("nil KEK accepted")
	}
	if _, err := envelope.New(env, env); err == nil {
		t.Fatal("duplicate KEK id accepted")
	}
}

// TestRotation: a new keyring version rewraps DEKs only, and old blobs
// keep opening until they are rewrapped.
func TestRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k1, k2 := key(t), key(t)
	old := sealer(t, envelope.FromKeyring(keyring(t, "1:"+k1)))
	aad := []byte("payments:paymob:store-a:conn-9")
	blob, err := old.Seal(ctx, []byte("hmac-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if old.NeedsRewrap(blob) {
		t.Fatal("fresh blob needs a rewrap")
	}

	rotated := sealer(t, envelope.FromKeyring(keyring(t, "2:"+k2+",1:"+k1)))
	if !rotated.NeedsRewrap(blob) {
		t.Fatal("blob of key 1 does not need a rewrap under key 2")
	}
	opens(t, rotated, blob, string(aad), "hmac-secret")
	re, err := rotated.Rewrap(ctx, blob, aad)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.NeedsRewrap(re) {
		t.Fatal("rewrapped blob still needs a rewrap")
	}
	only2 := sealer(t, envelope.FromKeyring(keyring(t, "2:"+k2)))
	opens(t, only2, re, string(aad), "hmac-secret")
	if _, err := only2.Open(ctx, blob, aad); !errors.Is(err, envelope.ErrOpen) {
		t.Fatalf("old blob opened without key 1: %v", err)
	}
	if _, err := rotated.Rewrap(ctx, blob, []byte("another row")); !errors.Is(err, envelope.ErrOpen) {
		t.Fatalf("rewrap with another AAD: %v", err)
	}
	if _, err := rotated.Rewrap(ctx, []byte("junk"), aad); !errors.Is(err, envelope.ErrOpen) {
		t.Fatalf("rewrap of junk: %v", err)
	}
	if !rotated.NeedsRewrap([]byte("junk")) {
		t.Fatal("junk does not need a rewrap")
	}
}

// TestMoveBetweenHolders: blobs of the environment KEK open after a move
// to another holder, and rewrap onto it.
func TestMoveBetweenHolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := envelope.FromKeyring(keyring(t, "1:"+key(t)))
	kms := otherKEK{envelope.FromKeyring(keyring(t, "1:"+key(t)))}
	blob, err := sealer(t, env).Seal(ctx, []byte("s"), []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	moved := sealer(t, kms, env)
	if !moved.NeedsRewrap(blob) {
		t.Fatal("env blob does not need a rewrap onto the new holder")
	}
	re, err := moved.Rewrap(ctx, blob, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	opens(t, sealer(t, kms), re, "a", "s")
	if _, err := sealer(t, kms).Open(ctx, blob, []byte("a")); !errors.Is(err, envelope.ErrOpen) {
		t.Fatalf("env blob opened by a sealer that does not know env: %v", err)
	}
}

// TestUnreachableHolder: a KEK outage is reported as itself, not as a
// forged blob.
func TestUnreachableHolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kr := keyring(t, "1:"+key(t))
	blob, err := sealer(t, envelope.FromKeyring(kr)).Seal(ctx, []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	down := sealer(t, downKEK{envelope.FromKeyring(kr)})
	if _, err := down.Open(ctx, blob, nil); !errors.Is(err, errDown) || errors.Is(err, envelope.ErrOpen) {
		t.Fatalf("Open err = %v, want the outage", err)
	}
	if _, err := down.Rewrap(ctx, blob, nil); !errors.Is(err, errDown) {
		t.Fatalf("Rewrap err = %v, want the outage", err)
	}
}

// FuzzOpen: no input panics, and nothing but a real blob opens.
func FuzzOpen(f *testing.F) {
	ctx := context.Background()
	s := sealer(f, envelope.FromKeyring(keyring(f, "1:"+key(f))))
	blob, err := s.Seal(ctx, []byte("secret"), []byte("aad"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(blob, []byte("aad"))
	f.Add([]byte{'E', 1, 3, 'e', 'n', 'v', 0, 1}, []byte(nil))
	f.Add([]byte{}, []byte("aad"))
	f.Fuzz(func(t *testing.T, b, aad []byte) {
		got, err := s.Open(ctx, b, aad)
		if err == nil && (!bytes.Equal(b, blob) || !bytes.Equal(aad, []byte("aad")) || string(got) != "secret") {
			t.Fatalf("forged blob opened: %x", b)
		}
		_ = s.NeedsRewrap(b)
	})
}
