// Command covercheck enforces per-package statement coverage from a Go
// cover profile (CLAUDE.md: >= 80% for domain + app packages).
//
//	go run ./internal/tools/covercheck -profile coverage.out -min 80 \
//	  -match '/internal/modules/[^/]+/internal/(domain|app)$'
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type patterns []*regexp.Regexp

func (p *patterns) String() string { return fmt.Sprint(*p) }

func (p *patterns) Set(s string) error {
	re, err := regexp.Compile(s)
	if err != nil {
		return fmt.Errorf("bad -match %q: %w", s, err)
	}
	*p = append(*p, re)
	return nil
}

type tally struct{ covered, total int }

func main() {
	var match patterns
	profile := flag.String("profile", "coverage.out", "cover profile")
	minPct := flag.Float64("min", 80, "minimum statement coverage per matched package")
	flag.Var(&match, "match", "regexp on import path; repeatable")
	flag.Parse()

	f, err := os.Open(*profile)
	if err != nil {
		fail(err)
	}
	pkgs, err := parse(f)
	_ = f.Close()
	if err != nil {
		fail(err)
	}
	if code := report(os.Stdout, pkgs, match, *minPct); code != 0 {
		os.Exit(code)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "covercheck:", err)
	os.Exit(2)
}

// parse aggregates statement counts per package. Blocks are deduplicated
// because one block can appear in several test binaries' profiles.
func parse(r io.Reader) (map[string]tally, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]block{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// file.go:12.3,14.5 numStmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		stmts, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("malformed line %q: %w", line, err)
		}
		b := blocks[fields[0]]
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[fields[0]] = b
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	pkgs := map[string]tally{}
	for pos, b := range blocks {
		file, _, _ := strings.Cut(pos, ":")
		t := pkgs[path.Dir(file)]
		t.total += b.stmts
		if b.covered {
			t.covered += b.stmts
		}
		pkgs[path.Dir(file)] = t
	}
	return pkgs, nil
}

func report(w io.Writer, pkgs map[string]tally, match patterns, minPct float64) int {
	names := make([]string, 0, len(pkgs))
	for p := range pkgs {
		names = append(names, p)
	}
	sort.Strings(names)
	code := 0
	for _, p := range names {
		enforced := false
		for _, re := range match {
			enforced = enforced || re.MatchString(p)
		}
		t := pkgs[p]
		pct := 100.0
		if t.total > 0 {
			pct = 100 * float64(t.covered) / float64(t.total)
		}
		mark := "    "
		if enforced {
			mark = "ok  "
			if pct < minPct {
				mark, code = "FAIL", 1
			}
		}
		_, _ = fmt.Fprintf(w, "%s %6.1f%%  %s\n", mark, pct, p)
	}
	if code != 0 {
		_, _ = fmt.Fprintf(w, "covercheck: packages marked FAIL are below %.0f%%\n", minPct)
	}
	return code
}
