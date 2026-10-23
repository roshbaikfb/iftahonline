package domain

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Statement limits (ADR-0028).
const (
	MaxStatementBytes = 1 << 20 // the platform's request body limit (HTTP_MAX_BODY_BYTES default)
	MaxStatementLines = 20000
	MaxLineErrors     = 100
	MaxReferenceLen   = 100
)

// ErrStatement is a statement that cannot be read at all (no header, no
// tracking or amount column, too large).
var ErrStatement = errors.New("shipping: unreadable statement")

// SettlementLine is one payout line of a carrier's statement.
type SettlementLine struct {
	Line           int // 1-based, the header is line 1
	TrackingNumber string
	Amount         money.Money
	SettledAt      time.Time
	Reference      string
}

// LineError is a statement line that was skipped, and why.
type LineError struct {
	Line    int
	Message string
}

// column names a statement may use, in search form (lower case, Arabic
// normalized, spaces for separators): our template's first, then names
// carriers' exports use.
var columns = map[string][]string{ //nolint:gochecknoglobals // constant table
	"tracking":  {"tracking number", "tracking", "tracking no", "trackingnumber", "awb", "awb number", "رقم التتبع", "رقم البوليصه", "رقم الشحنه"},
	"amount":    {"amount", "cod", "cod amount", "collected", "collected amount", "cash collected", "المبلغ", "المبلغ المحصل", "قيمه التحصيل"},
	"date":      {"settled at", "date", "settlement date", "transfer date", "cash out date", "payout date", "التاريخ", "تاريخ التحويل"},
	"reference": {"reference", "transfer id", "cash out id", "payout id", "transaction id", "رقم التحويل", "المرجع"},
}

var dateLayouts = []string{ //nolint:gochecknoglobals // constant list
	time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "02/01/2006 15:04", "02/01/2006", "2/1/2006", "02-01-2006",
}

// ParseStatement reads a settlement statement (CSV, comma or semicolon,
// UTF-8 with or without BOM) in the store's currency; dates without a
// zone are in loc. Bad lines are skipped and reported (at most
// MaxLineErrors); a statement without the tracking and amount columns is
// ErrStatement. A line without a date is settled at asOf.
func ParseStatement(r io.Reader, cur money.Currency, loc *time.Location, asOf time.Time) ([]SettlementLine, []LineError, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxStatementBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrStatement, err)
	}
	if len(raw) > MaxStatementBytes {
		return nil, nil, fmt.Errorf("%w: larger than %d KiB", ErrStatement, MaxStatementBytes>>10)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(raw) {
		return nil, nil, fmt.Errorf("%w: not UTF-8 (save it as CSV UTF-8)", ErrStatement)
	}
	cr := csv.NewReader(bytes.NewReader(raw))
	cr.Comma = delimiter(raw)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: no header line", ErrStatement)
	}
	idx := map[string]int{}
	for i, h := range header {
		key := headerKey(h)
		for col, names := range columns {
			if _, done := idx[col]; !done && contains(names, key) {
				idx[col] = i
			}
		}
	}
	if _, ok := idx["tracking"]; !ok {
		return nil, nil, fmt.Errorf("%w: no tracking number column", ErrStatement)
	}
	if _, ok := idx["amount"]; !ok {
		return nil, nil, fmt.Errorf("%w: no amount column", ErrStatement)
	}

	var lines []SettlementLine
	var bad []LineError
	fail := func(line int, msg string) {
		if len(bad) < MaxLineErrors {
			bad = append(bad, LineError{Line: line, Message: msg})
		}
	}
	for n := 2; ; n++ {
		rec, readErr := cr.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			fail(n, "unreadable line")
			continue
		}
		if blank(rec) {
			continue
		}
		if len(lines) >= MaxStatementLines {
			return nil, nil, fmt.Errorf("%w: more than %d lines", ErrStatement, MaxStatementLines)
		}
		l, msg := parseLine(rec, idx, cur, loc, asOf)
		if msg != "" {
			fail(n, msg)
			continue
		}
		l.Line = n
		lines = append(lines, l)
	}
	return lines, bad, nil
}

func parseLine(rec []string, idx map[string]int, cur money.Currency, loc *time.Location, asOf time.Time) (SettlementLine, string) {
	field := func(col string) string {
		i, ok := idx[col]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var l SettlementLine
	l.TrackingNumber = strings.Map(arabic.ASCIIDigit, field("tracking"))
	if l.TrackingNumber == "" || len(l.TrackingNumber) > 64 || strings.ContainsFunc(l.TrackingNumber, unicode.IsSpace) {
		return l, "missing or invalid tracking number"
	}
	amount, err := money.Parse(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(field("amount"), string(cur)), "ج.م")), cur)
	if err != nil {
		return l, "invalid amount"
	}
	l.Amount = amount
	if d := field("date"); d == "" {
		l.SettledAt = asOf
	} else if l.SettledAt, err = parseDate(strings.Map(arabic.ASCIIDigit, d), loc); err != nil {
		return l, "invalid date (use YYYY-MM-DD)"
	}
	l.Reference = field("reference")
	if utf8.RuneCountInString(l.Reference) > MaxReferenceLen {
		return l, "reference too long"
	}
	return l, ""
}

func parseDate(s string, loc *time.Location) (time.Time, error) {
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unknown date format")
}

// delimiter guesses the separator from the header line: semicolons are
// common in spreadsheets saved in Arabic locales.
func delimiter(raw []byte) rune {
	first, _, _ := bufio.NewReader(bytes.NewReader(raw)).ReadLine() //nolint:errcheck // an empty statement fails at the header
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		return ';'
	}
	return ','
}

func headerKey(h string) string {
	h = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.TrimSpace(h))
	return strings.Join(strings.Fields(arabic.NormalizeSearch(strings.ToLower(h))), " ")
}

func contains(names []string, key string) bool {
	for _, n := range names {
		if headerKey(n) == key {
			return true
		}
	}
	return false
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// CODCarrier is one carrier's line of the COD report.
type CODCarrier struct {
	ConnectionID ulid.ID
	Carrier      string
	Currency     money.Currency
	Delivered    int
	Expected     money.Money // what delivered parcels should bring in
	Settled      money.Money // what the carrier paid out for them
	Held         money.Money // still at the carrier
	Held0to7     money.Money
	Held8to14    money.Money
	Held15Plus   money.Money
	OverSettled  int // parcels paid out more than their COD
}

// HeldParcel is one delivered parcel whose cash is not (all) paid out.
type HeldParcel struct {
	ShipmentID     ulid.ID
	OrderID        ulid.ID
	Carrier        string
	TrackingNumber string
	Expected       money.Money
	Settled        money.Money
	DeliveredAt    time.Time
}
