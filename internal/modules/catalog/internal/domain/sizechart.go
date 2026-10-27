package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Size chart limits (ADR-0035): a chart fits a phone screen and a product
// page stays small.
const (
	MinSizeChartColumns   = 2 // the size and one measurement
	MaxSizeChartColumns   = 8
	MaxSizeChartRows      = 30
	MaxSizeChartNameLen   = 60
	MaxSizeChartCellLen   = 30
	MaxSizeChartsPerStore = 100
)

// SizeChart is a store's reusable size chart, in centimetres. Header names
// the columns (the first is the size); every row of Body is as long as
// Header.
type SizeChart struct {
	ID     ulid.ID
	Name   string // for the merchant only
	Header []string
	Body   [][]string
	// Products is how many live products use the chart (lists only).
	Products  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SizeChartInput creates or replaces a size chart.
type SizeChartInput struct {
	Name   string
	Header []string
	Body   [][]string
}

// ValidateSizeChart trims every text and checks the chart's shape,
// reporting all invalid fields at once (header[1], body[2][0]).
func ValidateSizeChart(in SizeChartInput) (SizeChartInput, error) {
	out := SizeChartInput{Name: strings.TrimSpace(in.Name)}
	var fields []errs.FieldError
	add := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}
	if fe, ok := checkText("name", out.Name, MaxSizeChartNameLen, false); !ok {
		fields = append(fields, fe)
	}
	if n := len(in.Header); n < MinSizeChartColumns || n > MaxSizeChartColumns {
		add("header", "count", fmt.Sprintf("a size chart needs %d to %d columns", MinSizeChartColumns, MaxSizeChartColumns))
	} else {
		out.Header = make([]string, n)
		for i, h := range in.Header {
			out.Header[i] = strings.TrimSpace(h)
			if fe, ok := checkText(fmt.Sprintf("header[%d]", i), out.Header[i], MaxSizeChartCellLen, false); !ok {
				fields = append(fields, fe)
			}
		}
	}
	if n := len(in.Body); n == 0 || n > MaxSizeChartRows {
		add("body", "count", fmt.Sprintf("a size chart needs 1 to %d rows", MaxSizeChartRows))
	} else {
		out.Body = make([][]string, n)
		for i, row := range in.Body {
			if out.Header != nil && len(row) != len(out.Header) {
				add(fmt.Sprintf("body[%d]", i), "count", fmt.Sprintf("each row needs one cell per column (%d)", len(out.Header)))
				continue
			}
			out.Body[i] = make([]string, len(row))
			for j, c := range row {
				out.Body[i][j] = strings.TrimSpace(c)
				// The first cell names the size; the others may be blank.
				if fe, ok := checkText(fmt.Sprintf("body[%d][%d]", i, j), out.Body[i][j], MaxSizeChartCellLen, j > 0); !ok {
					fields = append(fields, fe)
				}
			}
		}
	}
	if len(fields) > 0 {
		return SizeChartInput{}, errs.Validation(fields...)
	}
	return out, nil
}

// normalizeColors checks an option's colours: absent (not a colour
// option), or one "#rrggbb" or "" per value, lower-cased.
func normalizeColors(prefix string, colors []string, values int) ([]string, []errs.FieldError) {
	if colors == nil {
		return nil, nil
	}
	if len(colors) != values {
		return nil, []errs.FieldError{{Field: prefix + ".colors", Code: "count", Message: "a colour option needs one colour per value"}}
	}
	out := make([]string, len(colors))
	var fields []errs.FieldError
	for i, c := range colors {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" && !IsHexColor(c) {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("%s.colors[%d]", prefix, i), Code: "invalid",
				Message: `a colour must be "#rrggbb" or empty`})
		}
		out[i] = c
	}
	return out, fields
}

// IsHexColor reports whether s is "#rrggbb" in lower-case hex: the only
// colour form that reaches a storefront stylesheet.
func IsHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s[1:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
