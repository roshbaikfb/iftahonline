package password_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
)

// fast keeps tests quick; production uses DefaultParams.
func fast() password.Params { return password.Params{MemoryKiB: 8 * 1024, Time: 1, Threads: 1} }

func TestNewValidates(t *testing.T) {
	t.Parallel()
	for _, p := range []password.Params{{}, {MemoryKiB: 1024, Time: 1, Threads: 1}, {MemoryKiB: 8192, Time: 0, Threads: 1}, {MemoryKiB: 8192, Time: 1, Threads: 0}} {
		if _, err := password.New(p, 1); err == nil {
			t.Fatalf("%+v must be rejected", p)
		}
	}
	if _, err := password.New(fast(), 0); err == nil {
		t.Fatal("zero concurrency must be rejected")
	}
	if d := password.DefaultParams(); d.MemoryKiB != 19456 || d.Time != 2 || d.Threads != 1 {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestHashVerify(t *testing.T) {
	t.Parallel()
	h, err := password.New(fast(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	enc, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("encoding = %q", enc)
	}
	again, _ := h.Hash(ctx, "correct horse battery")
	if again == enc {
		t.Fatal("salts must differ")
	}
	ok, rehash, err := h.Verify(ctx, "correct horse battery", enc)
	if !ok || rehash || err != nil {
		t.Fatalf("verify = %v %v %v", ok, rehash, err)
	}
	if ok, _, err = h.Verify(ctx, "correct horse batterY", enc); ok || err != nil {
		t.Fatalf("wrong password = %v %v", ok, err)
	}

	// A hash made with weaker parameters verifies and asks for a rehash.
	stronger, err := password.New(password.Params{MemoryKiB: 16 * 1024, Time: 1, Threads: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := stronger.Verify(ctx, "correct horse battery", enc); !ok || !rehash || err != nil {
		t.Fatalf("old params = %v %v %v", ok, rehash, err)
	}

	if err := h.VerifyDummy(ctx, "anything at all"); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	t.Parallel()
	h, err := password.New(fast(), 1)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := h.Hash(t.Context(), "x")
	parts := strings.Split(good, "$")
	bad := []string{
		"", "plain", "$argon2i$v=19$m=8192,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=18$m=8192,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=99999999,t=1,p=1$" + parts[4] + "$" + parts[5], // 95 GiB: refused
		"$argon2id$v=19$m=8192,t=99,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8192,t=1,p=300$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8192,t=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8192,t=1,p=1,x=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=8192,t=1,p=1$!!$" + parts[5],
		"$argon2id$v=19$m=8192,t=1,p=1$" + parts[4] + "$c2hvcnQ",
		good + "$extra",
	}
	for _, s := range bad {
		if _, _, err := h.Verify(t.Context(), "x", s); !errors.Is(err, password.ErrMalformed) {
			t.Fatalf("%q: err = %v", s, err)
		}
	}
}

func FuzzVerifyDecode(f *testing.F) {
	h, err := password.New(fast(), 4)
	if err != nil {
		f.Fatal(err)
	}
	good, _ := h.Hash(context.Background(), "pw")
	f.Add(good)
	f.Add("$argon2id$v=19$m=1,t=1,p=1$AAAA$AAAA")
	f.Fuzz(func(t *testing.T, enc string) {
		// Never panics on any stored value. And an encoding that accepts
		// "pw" (necessarily a genuine argon2id hash of it) must reject a
		// different password: acceptance is never unconditional. Seeds
		// are produced per fuzz worker with fresh salts, so the property
		// must not compare against one particular hash.
		ok, _, err := h.Verify(t.Context(), "pw", enc)
		if err != nil && ok {
			t.Fatal("ok with an error")
		}
		if ok {
			if other, _, _ := h.Verify(t.Context(), "not pw", enc); other {
				t.Fatalf("hash %q accepts any password", enc)
			}
		}
	})
}

// TestConcurrencyIsBounded: with one slot held, another caller waits and
// gets 503 when its deadline passes, instead of allocating more memory.
func TestConcurrencyIsBounded(t *testing.T) {
	t.Parallel()
	h, err := password.New(password.Params{MemoryKiB: 64 * 1024, Time: 4, Threads: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = h.Hash(expired, "pw"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("expired ctx: err = %v", err)
	}
	if _, _, err = h.Verify(expired, "pw", "$argon2id$v=19$m=8192,t=1,p=1$c2FsdHNhbHQ$c2FsdHNhbHRzYWx0c2FsdA"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("expired ctx verify: err = %v", err)
	}

	// Hold the only slot with slow hashes, then ask with a short deadline.
	var wg sync.WaitGroup
	var started atomic.Bool
	wg.Go(func() {
		started.Store(true)
		for range 3 {
			_, _ = h.Hash(t.Context(), "pw")
		}
	})
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
	short, cancelShort := context.WithTimeout(t.Context(), 2*time.Millisecond)
	defer cancelShort()
	_, err = h.Hash(short, "pw")
	wg.Wait()
	if !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("busy slot: err = %v, want unavailable", err)
	}
}

func BenchmarkHashDefault(b *testing.B) {
	h, err := password.New(password.DefaultParams(), 1)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := h.Hash(b.Context(), "correct horse battery"); err != nil {
			b.Fatal(err)
		}
	}
}
