package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

const profile = `mode: atomic
example.com/m/a/x.go:1.1,2.1 3 1
example.com/m/a/x.go:3.1,4.1 1 0
example.com/m/a/x.go:3.1,4.1 1 2
example.com/m/b/y.go:1.1,2.1 4 0
example.com/m/b/y.go:3.1,4.1 1 5
`

func TestParseAndReport(t *testing.T) {
	t.Parallel()
	pkgs, err := parse(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if a := pkgs["example.com/m/a"]; a.covered != 4 || a.total != 4 {
		t.Fatalf("a = %+v (duplicate blocks must merge)", a)
	}
	if b := pkgs["example.com/m/b"]; b.covered != 1 || b.total != 5 {
		t.Fatalf("b = %+v", b)
	}

	tests := []struct {
		match []string
		code  int
		want  string
	}{
		{[]string{`/a$`}, 0, "ok    100.0%  example.com/m/a"},
		{[]string{`/b$`}, 1, "FAIL   20.0%  example.com/m/b"},
		{nil, 0, "      20.0%  example.com/m/b"},
	}
	for _, tt := range tests {
		var ps patterns
		for _, m := range tt.match {
			ps = append(ps, regexp.MustCompile(m))
		}
		var out bytes.Buffer
		if code := report(&out, pkgs, ps, 80); code != tt.code || !strings.Contains(out.String(), tt.want) {
			t.Errorf("match %v: code %d, out:\n%s", tt.match, code, out.String())
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"mode: set\nx.go:1.1,2.1 3\n", "mode: set\nx.go:1.1,2.1 a b\n"} {
		if _, err := parse(strings.NewReader(bad)); err == nil {
			t.Errorf("parse(%q) succeeded", bad)
		}
	}
	var ps patterns
	if err := ps.Set("("); err == nil {
		t.Error("bad regexp accepted")
	}
	if err := ps.Set("ok"); err != nil || ps.String() == "" {
		t.Error("Set failed")
	}
}
