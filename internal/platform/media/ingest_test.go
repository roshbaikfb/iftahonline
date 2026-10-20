package media

import (
	"context"
	"errors"
	"image/color"
	"sync"
	"testing"
	"time"
)

// memObjects is an in-memory bucket.
type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	failGet error
	failPut error
}

func newMem() *memObjects {
	return &memObjects{objects: map[string][]byte{}, types: map[string]string{}}
}

func (m *memObjects) PresignUpload(_ context.Context, key, _ string, maxBytes int64, ttl time.Duration) (Upload, error) {
	return Upload{URL: "https://s3.test", Fields: map[string]string{"key": key}, MaxBytes: maxBytes, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (m *memObjects) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet != nil {
		return nil, m.failGet
	}
	b, ok := m.objects[key]
	if !ok {
		return nil, ErrNoObject
	}
	return b, nil
}

func (m *memObjects) Put(_ context.Context, key, contentType string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut != nil {
		return m.failPut
	}
	m.objects[key], m.types[key] = data, contentType
	return nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func TestIngest(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxBytes: 1 << 20, MaxPixels: 1 << 20}
	key := func(f string) string { return "masters/logo." + f }

	objs := newMem()
	objs.objects["uploads/logo"] = encPNG(t, solid(40, 20, color.NRGBA{R: 200, A: 128}))
	m, k, err := Ingest(t.Context(), objs, lim, "uploads/logo", key)
	if err != nil || k != "masters/logo.png" || m.Width != 40 || m.Height != 20 || objs.types[k] != "image/png" || len(objs.objects[k]) == 0 {
		t.Fatalf("ingest: %v %q %+v", err, k, m)
	}
	if _, ok := objs.objects["uploads/logo"]; !ok {
		t.Fatal("the upload must be left for the caller to delete")
	}

	tests := []struct {
		name     string
		setup    func(*memObjects)
		wantCode string
		wantErr  error
	}{
		{"missing upload", func(*memObjects) {}, "upload_missing", nil},
		{"not an image", func(o *memObjects) { o.objects["uploads/logo"] = []byte("<svg onload=alert(1)>") }, "unsupported_type", nil},
		{"storage down on read", func(o *memObjects) { o.failGet = errBoom }, "", errBoom},
		{"storage down on write", func(o *memObjects) {
			o.objects["uploads/logo"] = encJPEG(t, solid(8, 8, color.White))
			o.failPut = errBoom
		}, "", errBoom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := newMem()
			tt.setup(o)
			_, _, err := Ingest(t.Context(), o, lim, "uploads/logo", key)
			code, rejected := IsReject(err)
			if tt.wantCode != "" && (!rejected || code != tt.wantCode) {
				t.Fatalf("err = %v, want reject %q", err, tt.wantCode)
			}
			if tt.wantErr != nil && (rejected || !errors.Is(err, tt.wantErr)) {
				t.Fatalf("err = %v, want %v (retryable)", err, tt.wantErr)
			}
		})
	}
}

var errBoom = errors.New("boom")
