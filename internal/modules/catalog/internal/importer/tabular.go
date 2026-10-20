// Package importer reads products from merchant sources (ADR-0014): CSV
// files (through a tabular layer any spreadsheet format can reuse) and
// the WooCommerce REST API. Every source is hostile input: it is size-bounded, never panics,
// and turns a bad row into a record error instead of failing the import.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Source yields products to import, in a stable order so an import can
// resume after a crash by skipping what it already handled.
type Source interface {
	// Total is the number of records, or -1 when unknown.
	Total() int
	// Next returns the next record, or io.EOF.
	Next(ctx context.Context) (domain.ImportRecord, error)
	// Skip passes over the next record without building it (resume).
	Skip(ctx context.Context) error
}

// SourceError means the source as a whole cannot be imported. Code is
// stable and shown to the merchant; retrying will not help.
type SourceError struct {
	Code   string
	Detail string
}

func (e *SourceError) Error() string { return "importer: " + e.Code + ": " + e.Detail }

func sourceErr(code, format string, args ...any) error {
	return &SourceError{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Limits on tabular files.
const (
	maxRows    = 50_000 // data rows (variants) per file
	maxColumns = 64
	maxCellLen = domain.MaxDescriptionLen
)

// Column names, with the Arabic headers merchants commonly use.
func columnAliases() map[string]string {
	m := map[string]string{
		"handle": "handle", "slug": "handle", "المعرف": "handle",
		"title": "title", "name": "title", "العنوان": "title", "الاسم": "title", "اسم المنتج": "title",
		"description": "description", "الوصف": "description",
		"status": "status", "الحالة": "status",
		"categories": "categories", "category": "categories", "التصنيف": "categories", "التصنيفات": "categories",
		"sku": "sku", "رمز المنتج": "sku",
		"price": "price", "السعر": "price",
		"compare_at_price": "compare_at_price", "السعر قبل الخصم": "compare_at_price",
		"stock": "stock", "quantity": "stock", "الكمية": "stock", "المخزون": "stock",
		"tracked": "tracked", "track_stock": "tracked",
	}
	for i := 1; i <= domain.MaxOptions; i++ {
		n := strconv.Itoa(i)
		m["option"+n+"_name"] = "option" + n + "_name"
		m["option"+n+"_value"] = "option" + n + "_value"
	}
	return m
}

// Tabular is a Source over rows of cells (a parsed CSV or worksheet).
// Rows with the same non-empty handle, one after another, are one product
// with several variants.
type Tabular struct {
	cols   map[string]int
	cur    money.Currency
	groups [][]row
	next   int
}

type row struct {
	line  int
	cells []string
}

// NewTabular reads the header and groups the rows. rows[0] is the header;
// row i is on line i+1.
func NewTabular(rows [][]string, cur money.Currency) (*Tabular, error) {
	lines := make([]int, len(rows))
	for i := range lines {
		lines[i] = i + 1
	}
	return newTabular(rows, lines, cur)
}

// newTabular is NewTabular with the file line of each row.
func newTabular(rows [][]string, lines []int, cur money.Currency) (*Tabular, error) {
	if len(rows) == 0 {
		return nil, sourceErr("empty_file", "the file has no header row")
	}
	if len(rows)-1 > maxRows {
		return nil, sourceErr("too_many_rows", "at most %d rows per file", maxRows)
	}
	t := &Tabular{cols: map[string]int{}, cur: cur}
	aliases := columnAliases()
	if len(rows[0]) > maxColumns {
		return nil, sourceErr("bad_header", "at most %d columns", maxColumns)
	}
	for i, h := range rows[0] {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		key = strings.ReplaceAll(key, " ", "_")
		if c, ok := aliases[key]; ok {
			t.cols[c] = i
		} else if c, ok := aliases[strings.ReplaceAll(key, "_", " ")]; ok {
			t.cols[c] = i
		}
	}
	if _, ok := t.cols["price"]; !ok {
		return nil, sourceErr("bad_header", "a price column is required")
	}
	if _, title := t.cols["title"]; !title {
		return nil, sourceErr("bad_header", "a title column is required")
	}
	t.group(rows[1:], lines[1:])
	if len(t.groups) > domain.MaxImportRecords {
		return nil, sourceErr("too_many_products", "at most %d products per import", domain.MaxImportRecords)
	}
	return t, nil
}

func (t *Tabular) group(rows [][]string, lines []int) {
	var cur []row
	curHandle := ""
	for i, cells := range rows {
		if blank(cells) {
			continue
		}
		r := row{line: lines[i], cells: cells}
		h := strings.TrimSpace(t.cell(r, "handle"))
		if len(cur) > 0 && h != "" && strings.EqualFold(h, curHandle) {
			cur = append(cur, r)
			continue
		}
		if len(cur) > 0 {
			t.groups = append(t.groups, cur)
		}
		cur, curHandle = []row{r}, h
	}
	if len(cur) > 0 {
		t.groups = append(t.groups, cur)
	}
}

func blank(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func (t *Tabular) cell(r row, col string) string {
	i, ok := t.cols[col]
	if !ok || i >= len(r.cells) {
		return ""
	}
	return r.cells[i]
}

// Total implements Source.
func (t *Tabular) Total() int { return len(t.groups) }

// Skip implements Source.
func (t *Tabular) Skip(context.Context) error {
	if t.next >= len(t.groups) {
		return io.EOF
	}
	t.next++
	return nil
}

// Next implements Source.
func (t *Tabular) Next(context.Context) (domain.ImportRecord, error) {
	if t.next >= len(t.groups) {
		return domain.ImportRecord{}, io.EOF
	}
	g := t.groups[t.next]
	t.next++
	rec, err := t.record(g)
	if err != nil {
		return domain.ImportRecord{Line: g[0].line, Err: err.Error()}, nil
	}
	return rec, nil
}

// record builds one product from its rows; the first row carries the
// product fields and option names.
func (t *Tabular) record(g []row) (domain.ImportRecord, error) {
	first := g[0]
	for _, r := range g {
		for _, c := range r.cells {
			if !utf8.ValidString(c) || utf8.RuneCountInString(c) > maxCellLen {
				return domain.ImportRecord{}, fmt.Errorf("line %d: a cell is not UTF-8 text or is too long", r.line)
			}
		}
	}
	p := domain.ProductInput{
		Slug:        t.cell(first, "handle"),
		Title:       t.cell(first, "title"),
		Description: t.cell(first, "description"),
		Status:      normalizeStatus(t.cell(first, "status")),
	}
	for i := 1; i <= domain.MaxOptions; i++ {
		if name := strings.TrimSpace(t.cell(first, fmt.Sprintf("option%d_name", i))); name != "" {
			p.Options = append(p.Options, domain.Option{Name: name})
		}
	}
	for _, r := range g {
		v, err := t.variant(r, p.Options)
		if err != nil {
			return domain.ImportRecord{}, err
		}
		p.Variants = append(p.Variants, v)
	}
	return domain.ImportRecord{Line: first.line, Product: p, Categories: splitCategories(t.cell(first, "categories"))}, nil
}

func (t *Tabular) variant(r row, opts []domain.Option) (domain.VariantInput, error) {
	v := domain.VariantInput{SKU: t.cell(r, "sku")}
	for i := range opts {
		val := strings.TrimSpace(t.cell(r, fmt.Sprintf("option%d_value", i+1)))
		v.OptionValues = append(v.OptionValues, val)
		if val != "" && !containsFold(opts[i].Values, val) {
			opts[i].Values = append(opts[i].Values, val)
		}
	}
	price, err := money.Parse(t.cell(r, "price"), t.cur)
	if err != nil {
		return v, fmt.Errorf("line %d: price: not an amount in %s", r.line, t.cur)
	}
	v.PriceMinor = price.Amount()
	if s := strings.TrimSpace(t.cell(r, "compare_at_price")); s != "" {
		c, err := money.Parse(s, t.cur)
		if err != nil {
			return v, fmt.Errorf("line %d: compare_at_price: not an amount in %s", r.line, t.cur)
		}
		amount := c.Amount()
		v.CompareAtMinor = &amount
	}
	if s := strings.TrimSpace(t.cell(r, "stock")); s != "" {
		n, err := strconv.Atoi(asciiDigits(s))
		if err != nil {
			return v, fmt.Errorf("line %d: stock: not a whole number", r.line)
		}
		v.OnHand = n
	}
	if s := strings.TrimSpace(t.cell(r, "tracked")); s != "" {
		b, ok := parseBool(s)
		if !ok {
			return v, fmt.Errorf("line %d: tracked: use yes or no", r.line)
		}
		v.Tracked = &b
	}
	return v, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// normalizeStatus maps common words to a product status; anything else
// is passed through for validation to report.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "draft", "مسودة":
		return string(domain.StatusDraft)
	case "active", "published", "publish", "نشط", "منشور":
		return string(domain.StatusActive)
	case "archived", "مؤرشف":
		return string(domain.StatusArchived)
	}
	return s
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "نعم":
		return true, true
	case "0", "false", "no", "n", "لا":
		return false, true
	}
	return false, false
}

func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x0660 && r <= 0x0669:
			return '0' + (r - 0x0660)
		case r >= 0x06F0 && r <= 0x06F9:
			return '0' + (r - 0x06F0)
		}
		return r
	}, s)
}

// splitCategories splits "Men | Shoes" (or ; and the Arabic ، ؛) into names.
func splitCategories(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '|' || r == ';' || r == '،' || r == '؛' }) {
		if p := strings.TrimSpace(part); p != "" && !containsFold(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// IsSourceError reports whether err is a *SourceError and returns its code.
func IsSourceError(err error) (string, bool) {
	var e *SourceError
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}
