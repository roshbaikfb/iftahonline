package noplannames

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const allowMarker = "noplannames:allow "

// planRow matches a row of `INSERT INTO plans (id, code, ...)`.
var planRow = regexp.MustCompile(`\(\s*'[0-9a-f-]{36}'\s*,\s*'([a-z_]+)'`)

// planField names a value that holds a plan's code or name.
var planField = regexp.MustCompile(`(?i)^plan_?(code|name)s?$`)

var skippedDirs = map[string]bool{ //nolint:gochecknoglobals // constant set
	".git": true, "node_modules": true, "bin": true, "dist": true, ".cache": true, "build": true,
	"test-results": true, "playwright-report": true, ".lighthouseci": true, ".dart_tool": true, "testdata": true,
}

// planCodes reads the codes the migrations seed plans with.
func planCodes(t *testing.T, root string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "db", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // paths from the repository
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(b), ";") {
			if !strings.Contains(stmt, "INSERT INTO plans ") {
				continue
			}
			for _, m := range planRow.FindAllStringSubmatch(stmt, -1) {
				if !slices.Contains(codes, m[1]) {
					codes = append(codes, m[1])
				}
			}
		}
	}
	if len(codes) < 6 {
		t.Fatalf("found plan codes %v in the migrations: is the seed's shape still (id, code, ...)?", codes)
	}
	return codes
}

// finding is one plan-name check.
type finding struct {
	line int
	what string
}

func names(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.IndexExpr:
		if lit, ok := v.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			return s
		}
	case *ast.ParenExpr:
		return names(v.X)
	}
	return ""
}

// checkGo reports the plan-name checks in a Go source file.
func checkGo(fset *token.FileSet, file *ast.File, codes []string) []finding {
	isCode := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return false
		}
		s, err := strconv.Unquote(lit.Value)
		return err == nil && slices.Contains(codes, strings.ToLower(s))
	}
	isPlanField := func(e ast.Expr) bool { return planField.MatchString(names(e)) }
	var out []finding
	add := func(n ast.Node, what string) { out = append(out, finding{fset.Position(n.Pos()).Line, what}) }
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BinaryExpr:
			if v.Op != token.EQL && v.Op != token.NEQ {
				break
			}
			if isCode(v.X) || isCode(v.Y) {
				add(v, "compares with a plan code")
			} else if isPlanField(v.X) || isPlanField(v.Y) {
				add(v, "compares a plan's code or name")
			}
		case *ast.SwitchStmt:
			if v.Tag != nil && isPlanField(v.Tag) {
				add(v, "switches on a plan's code or name")
			}
		case *ast.CaseClause:
			for _, e := range v.List {
				if isCode(e) {
					add(e, "a case on a plan code")
				}
			}
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" ||
				!slices.Contains([]string{"EqualFold", "HasPrefix", "HasSuffix", "Contains", "Compare"}, sel.Sel.Name) {
				break
			}
			for _, a := range v.Args {
				if isCode(a) {
					add(v, "strings."+sel.Sel.Name+" with a plan code")
				} else if isPlanField(a) {
					add(v, "strings."+sel.Sel.Name+" on a plan's code or name")
				}
			}
		}
		return true
	})
	return out
}

// tsChecks match dashboard comparisons that involve plans.
func tsChecks(codes []string) []*regexp.Regexp {
	alt := strings.Join(codes, "|")
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)plan\w*(\.\w+)*\s*[!=]==?\s*['"` + "`" + `](` + alt + `)['"` + "`" + `]`),
		regexp.MustCompile(`(?i)['"` + "`" + `](` + alt + `)['"` + "`" + `]\s*[!=]==?\s*plan`),
		regexp.MustCompile(`(?i)plan[._]?(code|name)\s*[!=]==?`),
		regexp.MustCompile(`(?i)[!=]==?\s*\w*\.?plan[._]?(code|name)\b`),
	}
}

func checkTS(src []byte, checks []*regexp.Regexp) []finding {
	var out []finding
	s := bufio.NewScanner(bytes.NewReader(src))
	s.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for n := 1; s.Scan(); n++ {
		for _, re := range checks {
			if re.MatchString(s.Text()) {
				out = append(out, finding{n, "compares a plan's code or name"})
				break
			}
		}
	}
	return out
}

func allowed(src []byte, line int) bool {
	lines := bytes.Split(src, []byte("\n"))
	if line < 1 || line > len(lines) {
		return false
	}
	l := string(lines[line-1])
	i := strings.Index(l, allowMarker)
	return i >= 0 && strings.TrimSpace(l[i+len(allowMarker):]) != ""
}

func TestChecks(t *testing.T) {
	t.Parallel()
	codes := []string{"free", "pays", "merchant", "pro", "brand", "agency"}
	bad := []string{
		`x := s.Plan == "pro"`,
		`x := "agency" != kind`,
		`x := strings.EqualFold(name, "Brand")`,
		`x := sub.PlanCode == other`,
		`x := planName != ""`,
		`x := m["plan_code"] == y`,
		`x := strings.HasPrefix(p.PlanCode, "x")`,
	}
	for _, src := range bad {
		if got := goFindings(t, "package p\nfunc f() { "+src+"; _ = x }", codes); len(got) != 1 {
			t.Errorf("%s: %d findings, want 1", src, len(got))
		}
	}
	switches := []string{
		`switch s.PlanCode { case "a": }`,
		`switch v { case "pays": }`,
	}
	for _, src := range switches {
		if got := goFindings(t, "package p\nfunc f(s struct{ PlanCode string }, v string) { "+src+" }", codes); len(got) != 1 {
			t.Errorf("%s: %d findings, want 1", src, len(got))
		}
	}
	good := []string{
		`x := s.Kind == "subscription"`, // a kind is no plan name
		`x := s.Status == "active"`,
		`x := plan == nil`,
		`x := strings.Contains(name, "professional")`,
		`x := len("pro") == 3`,
	}
	for _, src := range good {
		if got := goFindings(t, "package p\nfunc f() { "+src+"; _ = x }", codes); len(got) != 0 {
			t.Errorf("%s: false positive %+v", src, got)
		}
	}

	checks := tsChecks(codes)
	for _, line := range []string{
		`if (plan.code === 'pro') {`, `const paid = sub.plan !== "free"`, `'agency' == plan`,
		`if (planCode === x)`, `x = a !== b.plan_name`,
	} {
		if len(checkTS([]byte(line), checks)) != 1 {
			t.Errorf("%s: not caught", line)
		}
	}
	for _, line := range []string{
		`slugState.kind === 'free' ? 'ok' : 'bad'`, `<td>{plan.name}</td>`, `if (ent.ad_free) {`, `plans.map((p) => p.name)`,
	} {
		if len(checkTS([]byte(line), checks)) != 0 {
			t.Errorf("%s: false positive", line)
		}
	}
	if !allowed([]byte("a\nx == \"merchant\" // noplannames:allow order source\n"), 2) || allowed([]byte("x // noplannames:allow \n"), 1) {
		t.Error("allow marker")
	}
}

func goFindings(t *testing.T, src string, codes []string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return checkGo(fset, f, codes)
}

// TestNoPlanNameChecks walks the repository and fails on any plan-name
// check outside the billing module.
func TestNoPlanNameChecks(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	codes := planCodes(t, root)
	billing := filepath.Join(root, "internal", "modules", "billing")
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	checks := tsChecks(codes)
	goFiles, tsFiles := 0, 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == billing || path == self || (path != root && skippedDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".ts" && ext != ".tsx" {
			return nil
		}
		src, rErr := os.ReadFile(path) //nolint:gosec // paths come from walking the repository
		if rErr != nil {
			return rErr
		}
		var found []finding
		if ext == ".go" {
			goFiles++
			fset := token.NewFileSet()
			f, pErr := parser.ParseFile(fset, path, src, 0)
			if pErr != nil {
				return pErr
			}
			found = checkGo(fset, f, codes)
		} else {
			tsFiles++
			found = checkTS(src, checks)
		}
		rel, _ := filepath.Rel(root, path)
		for _, fd := range found {
			if !allowed(src, fd.line) {
				t.Errorf("%s:%d: %s (ask entitlements for a feature instead; ADR-0045)", rel, fd.line, fd.what)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if goFiles < 300 || tsFiles < 50 {
		t.Fatalf("scanned %d Go and %d TypeScript files from %s: is the root right?", goFiles, tsFiles, root)
	}
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
