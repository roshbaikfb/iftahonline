// Package migsafe checks that migrations do not lock a big table while
// they scan it (CLAUDE.md: migrations are backwards compatible, expand
// then migrate then contract).
//
// ALTER TABLE ... ADD a CHECK or a FOREIGN KEY validates every existing
// row under an ACCESS EXCLUSIVE lock: on orders, order_events and their
// partitions, writes stop for as long as the scan takes. Add them NOT
// VALID, then VALIDATE CONSTRAINT in a later migration (which takes a
// lock that lets writes through). A table created in the same migration
// is empty, so its own ALTERs are fine; anything else known to be small
// may say why on the line: "-- migsafe:allow <reason>".
package migsafe

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// baseline is the last migration written before this check: those ran
// on databases without production data and are not edited (goose does
// not run a changed migration again).
const baseline = 35

const allowMarker = "migsafe:allow "

var (
	alterAdd  = regexp.MustCompile(`(?is)\bALTER\s+TABLE\s+(?:ONLY\s+)?(?:IF\s+EXISTS\s+)?([a-z0-9_."]+)\s+(.*?);`) //nolint:gochecknoglobals // compiled once
	validates = regexp.MustCompile(`(?is)\bADD\b[^,]*?\b(CHECK|FOREIGN\s+KEY|REFERENCES)\b`)                        //nolint:gochecknoglobals // compiled once
	notValid  = regexp.MustCompile(`(?i)\bNOT\s+VALID\b`)                                                           //nolint:gochecknoglobals // compiled once
	creates   = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_."]+)`)                //nolint:gochecknoglobals // compiled once
)

// problems lists the ALTERs of up that validate existing rows under a
// lock.
func problems(up string) []string {
	created := map[string]bool{}
	for _, m := range creates.FindAllStringSubmatch(up, -1) {
		created[strings.ToLower(strings.Trim(m[1], `"`))] = true
	}
	var out []string
	for _, ix := range alterAdd.FindAllStringSubmatchIndex(up, -1) {
		stmt, table, rest := up[ix[0]:ix[1]], strings.ToLower(strings.Trim(up[ix[2]:ix[3]], `"`)), up[ix[4]:ix[5]]
		lineEnd := strings.IndexByte(up[ix[1]:], '\n')
		if lineEnd < 0 {
			lineEnd = len(up) - ix[1]
		}
		if created[table] || strings.Contains(stmt+up[ix[1]:ix[1]+lineEnd], allowMarker) {
			continue
		}
		if validates.MatchString(rest) && !notValid.MatchString(rest) {
			out = append(out, strings.Join(strings.Fields(stmt), " "))
		}
	}
	return out
}

// upSection is a goose migration's Up part, without comments other than
// the allow marker.
func upSection(sql string) string {
	up, _, _ := strings.Cut(sql, "-- +goose Down")
	var b strings.Builder
	for line := range strings.SplitSeq(up, "\n") {
		code, comment, _ := strings.Cut(line, "--")
		b.WriteString(code)
		if strings.Contains(comment, allowMarker) {
			b.WriteString(" --" + comment)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPatterns(t *testing.T) {
	t.Parallel()
	bad := []string{
		"ALTER TABLE orders ADD CONSTRAINT x CHECK (total_minor >= 0);",
		"ALTER TABLE orders ADD COLUMN fee bigint NOT NULL DEFAULT 0 CHECK (fee >= 0);",
		"ALTER TABLE order_events ADD CONSTRAINT f FOREIGN KEY (store_id) REFERENCES stores (id);",
		"alter table orders\n  add column a int,\n  add column b text check (b <> '');",
	}
	for _, s := range bad {
		if len(problems(upSection(s))) == 0 {
			t.Errorf("not caught: %q", s)
		}
	}
	good := []string{
		"ALTER TABLE orders ADD CONSTRAINT x CHECK (total_minor >= 0) NOT VALID;",
		"ALTER TABLE orders VALIDATE CONSTRAINT x;",
		"ALTER TABLE orders ADD COLUMN note text NOT NULL DEFAULT '';",
		"CREATE TABLE refunds (id uuid PRIMARY KEY);\nALTER TABLE refunds ADD CONSTRAINT r CHECK (true);",
		"ALTER TABLE stores ADD CONSTRAINT x CHECK (true); -- migsafe:allow stores is small (one row per store)",
		"ALTER TABLE orders DROP CONSTRAINT x;",
		"ALTER TABLE orders ENABLE ROW LEVEL SECURITY;",
		"-- ALTER TABLE orders ADD CONSTRAINT x CHECK (true);",
	}
	for _, s := range good {
		if p := problems(upSection(s)); len(p) > 0 {
			t.Errorf("false positive %q: %v", s, p)
		}
	}
}

// TestMigrations scans every migration after the baseline.
func TestMigrations(t *testing.T) {
	t.Parallel()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		name := e.Name()
		n, convErr := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if convErr != nil || !strings.HasSuffix(name, ".sql") {
			continue
		}
		seen++
		if n <= baseline {
			continue
		}
		b, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // our own migrations
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, p := range problems(upSection(string(b))) {
			t.Errorf("%s: %q validates existing rows under a lock: add it NOT VALID and VALIDATE it in a later migration (or mark the line %q with why the table is small)",
				name, p, "-- "+allowMarker+"<reason>")
		}
	}
	if seen < baseline {
		t.Fatalf("found %d migrations in %s, want at least %d", seen, dir, baseline)
	}
}
