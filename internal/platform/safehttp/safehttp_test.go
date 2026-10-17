package safehttp_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
)

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func get(t *testing.T, c *http.Client, rawURL string) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestBlocksLoopbackByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }))
	defer srv.Close()
	c := safehttp.New(safehttp.Options{AllowedPorts: []int{serverPort(t, srv)}})

	if _, err := get(t, c, srv.URL); !errors.Is(err, safehttp.ErrBlockedAddress) {
		t.Fatalf("loopback by IP: err = %v", err)
	}
	// A hostname that resolves to loopback is caught after DNS (rebinding).
	host := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, err := get(t, c, host); !errors.Is(err, safehttp.ErrBlockedAddress) {
		t.Fatalf("loopback by name: err = %v", err)
	}
}

func TestAllowPrefixAndRedirectToPrivate(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") })
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := safehttp.New(safehttp.Options{
		AllowedPorts:  []int{serverPort(t, srv), 80},
		AllowPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		MaxRedirects:  3,
	})

	if body, err := get(t, c, srv.URL+"/ok"); err != nil || body != "hello" {
		t.Fatalf("allowed request: %q, %v", body, err)
	}
	if _, err := get(t, c, srv.URL+"/to-metadata"); !errors.Is(err, safehttp.ErrBlockedAddress) {
		t.Fatalf("redirect to metadata: err = %v", err)
	}
	if _, err := get(t, c, srv.URL+"/to-file"); !errors.Is(err, safehttp.ErrScheme) {
		t.Fatalf("redirect to file: err = %v", err)
	}
	if _, err := get(t, c, srv.URL+"/loop"); !errors.Is(err, safehttp.ErrTooManyRedirects) {
		t.Fatalf("redirect loop: err = %v", err)
	}
}

func TestResponseLimit(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/declared", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, _ *http.Request) {
		for range 4 {
			_, _ = io.WriteString(w, strings.Repeat("y", 512))
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/exact", func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("z", 1024))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := safehttp.New(safehttp.Options{
		AllowedPorts:     []int{serverPort(t, srv)},
		AllowPrefixes:    []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		MaxResponseBytes: 1024,
	})
	if _, err := get(t, c, srv.URL+"/declared"); !errors.Is(err, safehttp.ErrResponseTooLarge) {
		t.Fatalf("declared: err = %v", err)
	}
	if _, err := get(t, c, srv.URL+"/chunked"); !errors.Is(err, safehttp.ErrResponseTooLarge) {
		t.Fatalf("chunked: err = %v", err)
	}
	if body, err := get(t, c, srv.URL+"/exact"); err != nil || len(body) != 1024 {
		t.Fatalf("exact: len %d, err %v", len(body), err)
	}
}

func TestSchemeAndNoRedirects(t *testing.T) {
	t.Parallel()
	c := safehttp.New(safehttp.Options{MaxRedirects: -1})
	for _, u := range []string{"ftp://example.com/", "gopher://example.com/"} {
		if _, err := get(t, c, u); !errors.Is(err, safehttp.ErrScheme) {
			t.Errorf("%s: err = %v", u, err)
		}
	}
}

func TestValidateURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw  string
		want error
	}{
		{"https://hooks.example.com/x", nil},
		{"http://example.com:8080/x", nil},
		{"https://example.com:22/x", safehttp.ErrBlockedPort},
		{"https://127.0.0.1/x", safehttp.ErrBlockedAddress},
		{"https://[::1]/x", safehttp.ErrBlockedAddress},
		{"http://169.254.169.254/", safehttp.ErrBlockedAddress},
		{"https://user:pass@example.com/", safehttp.ErrScheme},
		{"javascript:alert(1)", safehttp.ErrScheme},
		{"/relative", safehttp.ErrScheme},
		{"https://" + strings.Repeat("a", 2100) + ".com", safehttp.ErrScheme},
	}
	for _, tt := range tests {
		t.Run(tt.raw[:min(len(tt.raw), 40)], func(t *testing.T) {
			t.Parallel()
			_, err := safehttp.ValidateURL(tt.raw, safehttp.Options{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := safehttp.ValidateURL("http://a b", safehttp.Options{}); err == nil {
		t.Fatal("unparsable URL must fail")
	}
}

// FuzzValidateURL: never panics, and anything accepted is http(s) with a
// host and an allowed port.
func FuzzValidateURL(f *testing.F) {
	for _, s := range []string{"https://example.com/", "http://127.0.0.1:80", "https://[::ffff:10.0.0.1]/", "http://0x7f.1/", "https://example.com:99999/", "//x", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := safehttp.ValidateURL(raw, safehttp.Options{})
		if err != nil {
			return
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			t.Fatalf("accepted %q -> %+v", raw, u)
		}
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
			ip = ip.Unmap()
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				t.Fatalf("accepted internal address %q", raw)
			}
		}
	})
}
