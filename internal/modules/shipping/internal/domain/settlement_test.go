package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

var (
	cairo = func() *time.Location { //nolint:gochecknoglobals // test fixture
		l, err := time.LoadLocation("Africa/Cairo")
		if err != nil {
			panic(err)
		}
		return l
	}()
	asOf = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture
)

func parse(t *testing.T, csv string) ([]domain.SettlementLine, []domain.LineError) {
	t.Helper()
	lines, bad, err := domain.ParseStatement(strings.NewReader(csv), money.EGP, cairo, asOf)
	if err != nil {
		t.Fatal(err)
	}
	return lines, bad
}

func TestParseStatementTemplate(t *testing.T) {
	t.Parallel()
	lines, bad := parse(t, "\ufefftracking_number,amount,settled_at,reference\n"+
		"7000001,520.00,2026-10-20,CO-1\n"+
		"7000002,\"1,060.50\",2026-10-21 14:30,CO-1\n"+
		"\n"+ // blank lines are skipped
		"7000003,-25,,ADJ-9\n")
	if len(bad) != 0 || len(lines) != 3 {
		t.Fatalf("lines %+v, bad %+v", lines, bad)
	}
	l := lines[0]
	if l.Line != 2 || l.TrackingNumber != "7000001" || l.Amount.Amount() != 52000 || l.Reference != "CO-1" ||
		!l.SettledAt.Equal(time.Date(2026, 10, 19, 21, 0, 0, 0, time.UTC)) { // midnight in Cairo
		t.Fatalf("line 1: %+v", l)
	}
	if lines[1].Amount.Amount() != 106050 || lines[2].Amount.Amount() != -2500 || !lines[2].SettledAt.Equal(asOf) {
		t.Fatalf("lines 2-3: %+v", lines[1:])
	}
}

// TestParseStatementExports: carriers' column names (Arabic too),
// semicolons, Arabic digits, a currency after the amount, day-first
// dates, columns in any order.
func TestParseStatementExports(t *testing.T) {
	t.Parallel()
	lines, bad := parse(t, "التاريخ;رقم التتبع;المبلغ المحصّل;رقم التحويل\n"+
		"20/10/2026;٧٠٠٠٠٠١;٥٢٠ ج.م;T-77\n"+
		"21-10-2026;7000002;99.5 EGP;T-77\n")
	if len(bad) != 0 || len(lines) != 2 || lines[0].TrackingNumber != "7000001" || lines[0].Amount.Amount() != 52000 ||
		lines[1].Amount.Amount() != 9950 || lines[0].Reference != "T-77" {
		t.Fatalf("lines %+v, bad %+v", lines, bad)
	}
	lines, _ = parse(t, "AWB Number,Cash Collected,Transfer Date\n7000009,100,2026-10-01T10:00:00Z\n")
	if len(lines) != 1 || lines[0].Amount.Amount() != 10000 || lines[0].Reference != "" {
		t.Fatalf("English export: %+v", lines)
	}
}

func TestParseStatementBadLines(t *testing.T) {
	t.Parallel()
	lines, bad := parse(t, "tracking,amount,date\n"+
		",520,2026-10-20\n"+ // no tracking number
		"a b,520,2026-10-20\n"+ // spaces
		"7000001,five hundred,2026-10-20\n"+ // amount
		"7000001,520.123,2026-10-20\n"+ // too many fraction digits: no silent rounding
		"7000001,520,next week\n"+ // date
		"7000002,520,2026-10-20\n")
	if len(lines) != 1 || lines[0].TrackingNumber != "7000002" || len(bad) != 5 || bad[0].Line != 2 || bad[4].Line != 6 {
		t.Fatalf("lines %+v, bad %+v", lines, bad)
	}
}

func TestParseStatementRefusals(t *testing.T) {
	t.Parallel()
	for name, csv := range map[string]string{
		"empty":          "",
		"no tracking":    "amount,date\n520,2026-10-20\n",
		"no amount":      "tracking,date\n7000001,2026-10-20\n",
		"not utf-8":      "tracking,amount\n\xff\xfe,1\n",
		"too large":      "tracking,amount\n" + strings.Repeat("7000001,1\n", domain.MaxStatementBytes/10+1),
		"too many lines": "tracking,amount\n" + strings.Repeat("7000001,1\n", domain.MaxStatementLines+1),
	} {
		if _, _, err := domain.ParseStatement(strings.NewReader(csv), money.EGP, cairo, asOf); !errors.Is(err, domain.ErrStatement) {
			t.Errorf("%s: %v, want ErrStatement", name, err)
		}
	}
	many := "tracking,amount\n" + strings.Repeat(",1\n", domain.MaxLineErrors+50)
	if _, bad, err := domain.ParseStatement(strings.NewReader(many), money.EGP, cairo, asOf); err != nil || len(bad) != domain.MaxLineErrors {
		t.Fatalf("errors kept: %d %v, want %d", len(bad), err, domain.MaxLineErrors)
	}
}

// FuzzParseStatement: no input panics, and every line it accepts is
// usable as is.
func FuzzParseStatement(f *testing.F) {
	f.Add("tracking_number,amount,settled_at,reference\n7000001,520.00,2026-10-20,CO-1\n")
	f.Add("رقم التتبع;المبلغ\n٧٠٠٠٠٠١;٥٢٠\n")
	f.Add("tracking,amount\n\"7000001,520\n")
	f.Add("\ufeff")
	f.Fuzz(func(t *testing.T, csv string) {
		lines, bad, err := domain.ParseStatement(strings.NewReader(csv), money.EGP, cairo, asOf)
		if err != nil {
			return
		}
		if len(bad) > domain.MaxLineErrors {
			t.Fatalf("%d line errors kept", len(bad))
		}
		for _, l := range lines {
			if l.TrackingNumber == "" || len(l.TrackingNumber) > 64 || l.SettledAt.IsZero() || l.Amount.Currency() != money.EGP {
				t.Fatalf("accepted %+v from %q", l, csv)
			}
		}
	})
}
