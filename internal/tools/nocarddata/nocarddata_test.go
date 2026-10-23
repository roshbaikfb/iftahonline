package nocarddata

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cardData matches names of card data in identifiers, columns, JSON
// properties and form inputs: card numbers (PAN), security codes,
// expiry dates and magnetic-stripe tracks.
var cardData = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`card[_-]?(number|num|no|pan)\b`,
	`\bpan\b`,
	`\bcc[_-]?(number|num|csc|cvv|exp)`,
	`\bcvv2?\b`, `\bcvc2?\b`,
	`(card|cc)[_-]?(exp|expiry|expiration)`,
	`\bexp(iry|iration)?[_-]?(month|year)\b`,
	`\btrack[_-]?(1|2|data)\b`,
	`security[_-]?code`,
}, "|"))

const allowMarker = "nocarddata:allow "

// Source kinds that can declare data: code, schema, contract, templates,
// the dashboard and the mobile app.
var scanned = map[string]bool{ //nolint:gochecknoglobals // constant set
	".go": true, ".sql": true, ".yaml": true, ".yml": true, ".templ": true,
	".ts": true, ".tsx": true, ".js": true, ".html": true, ".dart": true,
}

var skippedDirs = map[string]bool{ //nolint:gochecknoglobals // constant set
	".git": true, "node_modules": true, "bin": true, "dist": true, ".cache": true, "build": true,
	"test-results": true, "playwright-report": true, ".lighthouseci": true, ".dart_tool": true,
	"testdata": true, // fuzz corpora are random bytes
}

func TestPatterns(t *testing.T) {
	t.Parallel()
	bad := []string{
		"card_number text NOT NULL", "CardNumber string", "cardNumber: string", `name="card-number"`,
		`autocomplete="cc-number"`, "cc-csc", "cvv", "CVC2", "exp_month", "ExpiryYear int", "card_exp",
		"track2", "security_code", "pan text", "PAN", "card_pan",
	}
	for _, s := range bad {
		if !cardData.MatchString(s) {
			t.Errorf("%q is not caught", s)
		}
	}
	good := []string{
		"Japan", "panel", "expanded", "company", "expires_at", "expiry", "cardinality", "card_count",
		"discard", "tracking_number", "span", "expensive", "precc", "scc_number",
	}
	for _, s := range good {
		if cardData.MatchString(s) {
			t.Errorf("%q is a false positive", s)
		}
	}
}

// TestNoCardData walks the repository and fails on any card-data name
// outside a line marked allowed.
func TestNoCardData(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	self, err := filepath.Abs("nocarddata_test.go")
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !scanned[filepath.Ext(path)] || path == self || strings.HasSuffix(path, ".min.js") {
			return nil
		}
		files++
		return scan(t, root, path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 100 {
		t.Fatalf("scanned only %d files from %s: is the root right?", files, root)
	}
}

func scan(t *testing.T, root, path string) error {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // paths come from walking the repository
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if m := cardData.FindString(line); m != "" {
			if i := strings.Index(line, allowMarker); i >= 0 && strings.TrimSpace(line[i+len(allowMarker):]) != "" {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d: card data name %q (shoppers pay on the gateway's hosted page; see ADR-0024)", rel, n, m)
		}
	}
	return s.Err()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
