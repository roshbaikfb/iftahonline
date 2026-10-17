package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

func mustNew(t *testing.T, amount int64, c money.Currency) money.Money {
	t.Helper()
	m, err := money.New(amount, c)
	if err != nil {
		t.Fatalf("New(%d, %s): %v", amount, c, err)
	}
	return m
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cur     money.Currency
		wantErr error
	}{
		{"EGP", money.EGP, nil},
		{"SAR", money.SAR, nil},
		{"AED", money.AED, nil},
		{"unknown", money.Currency("USD"), money.ErrUnknownCurrency},
		{"empty", money.Currency(""), money.ErrUnknownCurrency},
		{"lowercase", money.Currency("egp"), money.ErrUnknownCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := money.New(100, tt.cur)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (m.Amount() != 100 || m.Currency() != tt.cur) {
				t.Fatalf("got %v", m)
			}
		})
	}
}

func TestZeroValueIsInvalid(t *testing.T) {
	t.Parallel()
	var m money.Money
	if _, err := m.Add(mustNew(t, 1, money.EGP)); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("zero Money must be rejected, got %v", err)
	}
}

func TestAddSub(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		a, b    int64
		ca, cb  money.Currency
		add     int64
		sub     int64
		wantErr error
	}{
		{"simple", 150, 50, money.EGP, money.EGP, 200, 100, nil},
		{"negative", -150, 50, money.SAR, money.SAR, -100, -200, nil},
		{"zero", 0, 0, money.AED, money.AED, 0, 0, nil},
		{"mismatch", 1, 1, money.EGP, money.SAR, 0, 0, money.ErrCurrencyMismatch},
		{"overflow", math.MaxInt64, 1, money.EGP, money.EGP, 0, 0, money.ErrOverflow},
		{"underflow", math.MinInt64, -1, money.EGP, money.EGP, 0, 0, money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, b := mustNew(t, tt.a, tt.ca), mustNew(t, tt.b, tt.cb)
			sum, err := a.Add(b)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Add err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && sum.Amount() != tt.add {
				t.Fatalf("Add = %d, want %d", sum.Amount(), tt.add)
			}
			if tt.wantErr != nil {
				return
			}
			diff, err := a.Sub(b)
			if err != nil {
				t.Fatalf("Sub err = %v", err)
			}
			if diff.Amount() != tt.sub {
				t.Fatalf("Sub = %d, want %d", diff.Amount(), tt.sub)
			}
		})
	}
}

func TestSubOverflow(t *testing.T) {
	t.Parallel()
	_, err := mustNew(t, math.MinInt64, money.EGP).Sub(mustNew(t, 1, money.EGP))
	if !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err = %v, want ErrOverflow", err)
	}
	_, err = mustNew(t, math.MaxInt64, money.EGP).Sub(mustNew(t, -1, money.EGP))
	if !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("err = %v, want ErrOverflow", err)
	}
}

func TestMulIntNeg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		a, n    int64
		want    int64
		wantErr error
	}{
		{"simple", 250, 4, 1000, nil},
		{"negative factor", 250, -2, -500, nil},
		{"zero", 250, 0, 0, nil},
		{"overflow", math.MaxInt64/2 + 1, 2, 0, money.ErrOverflow},
		{"minint * -1", math.MinInt64, -1, 0, money.ErrOverflow},
		{"minint * 1", math.MinInt64, 1, math.MinInt64, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustNew(t, tt.a, money.EGP).MulInt(tt.n)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got.Amount() != tt.want {
				t.Fatalf("got %d, want %d", got.Amount(), tt.want)
			}
		})
	}
	if _, err := mustNew(t, math.MinInt64, money.EGP).Neg(); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("Neg(MinInt64) err = %v", err)
	}
	n, err := mustNew(t, 5, money.EGP).Neg()
	if err != nil || n.Amount() != -5 {
		t.Fatalf("Neg(5) = %v, %v", n, err)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()
	a, b := mustNew(t, 1, money.EGP), mustNew(t, 2, money.EGP)
	for _, tc := range []struct {
		x, y money.Money
		want int
	}{{a, b, -1}, {b, a, 1}, {a, a, 0}} {
		got, err := tc.x.Cmp(tc.y)
		if err != nil || got != tc.want {
			t.Fatalf("Cmp(%v,%v) = %d, %v; want %d", tc.x, tc.y, got, err, tc.want)
		}
	}
	if _, err := a.Cmp(mustNew(t, 1, money.AED)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Cmp mismatch err = %v", err)
	}
	if !mustNew(t, 0, money.EGP).IsZero() || !mustNew(t, -1, money.EGP).IsNegative() || a.IsNegative() {
		t.Fatal("IsZero/IsNegative wrong")
	}
}

func TestPercentBP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		amount  int64
		bp      int64
		mode    money.RoundingMode
		want    int64
		wantErr error
	}{
		{"14% VAT EGP", 10000, 1400, money.RoundHalfUp, 1400, nil},
		{"15% VAT SAR exact", 9999, 1500, money.RoundHalfUp, 1500, nil}, // 1499.85 -> 1500
		{"5% VAT AED half up", 10, 500, money.RoundHalfUp, 1, nil},      // 0.5 -> 1
		{"half even down", 10, 500, money.RoundHalfEven, 0, nil},        // 0.5 -> 0
		{"half even up", 30, 500, money.RoundHalfEven, 2, nil},          // 1.5 -> 2
		{"down truncates", 19, 500, money.RoundDown, 0, nil},            // 0.95 -> 0
		{"negative half up", -10, 500, money.RoundHalfUp, -1, nil},      // -0.5 -> -1 (away from zero)
		{"negative half even", -30, 500, money.RoundHalfEven, -2, nil},  // -1.5 -> -2
		{"negative down", -19, 500, money.RoundDown, 0, nil},            // -0.95 -> 0
		{"100%", 12345, 10000, money.RoundHalfUp, 12345, nil},           // identity
		{"large no overflow", math.MaxInt64, 5000, money.RoundDown, math.MaxInt64 / 2, nil},
		{"overflow", math.MaxInt64, 20000, money.RoundDown, 0, money.ErrOverflow},
		{"bad mode", 100, 100, money.RoundingMode(99), 0, money.ErrInvalidRounding},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustNew(t, tt.amount, money.EGP).PercentBP(tt.bp, tt.mode)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got.Amount() != tt.want {
				t.Fatalf("got %d, want %d", got.Amount(), tt.want)
			}
		})
	}
}

func TestAllocate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		amount  int64
		ratios  []int64
		want    []int64
		wantErr error
	}{
		{"even thirds", 100, []int64{1, 1, 1}, []int64{34, 33, 33}, nil},
		{"weighted", 1000, []int64{70, 20, 10}, []int64{700, 200, 100}, nil},
		{"remainder", 5, []int64{1, 1}, []int64{3, 2}, nil},
		{"negative remainder", -5, []int64{1, 1}, []int64{-3, -2}, nil},
		{"zero ratio", 10, []int64{0, 1}, []int64{0, 10}, nil},
		{"huge amount", math.MaxInt64, []int64{1, 1}, []int64{math.MaxInt64/2 + 1, math.MaxInt64 / 2}, nil},
		{"no ratios", 10, nil, nil, money.ErrInvalidRatio},
		{"all zero", 10, []int64{0, 0}, nil, money.ErrInvalidRatio},
		{"negative ratio", 10, []int64{-1, 2}, nil, money.ErrInvalidRatio},
		{"ratio sum overflow", 10, []int64{math.MaxInt64, 1}, nil, money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parts, err := mustNew(t, tt.amount, money.SAR).Allocate(tt.ratios...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(parts) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(parts), len(tt.want))
			}
			var sum int64
			for i, p := range parts {
				if p.Amount() != tt.want[i] {
					t.Errorf("part %d = %d, want %d", i, p.Amount(), tt.want[i])
				}
				if p.Currency() != money.SAR {
					t.Errorf("part %d currency = %s", i, p.Currency())
				}
				sum += p.Amount()
			}
			if sum != tt.amount {
				t.Fatalf("parts sum to %d, want %d (minor units lost)", sum, tt.amount)
			}
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		cur     money.Currency
		want    int64
		wantErr bool
	}{
		{"0", money.EGP, 0, false},
		{"12", money.EGP, 1200, false},
		{"12.5", money.EGP, 1250, false},
		{"12.50", money.EGP, 1250, false},
		{"1,234.50", money.EGP, 123450, false},
		{"1,234,567", money.SAR, 123456700, false},
		{"-1,234.05", money.AED, -123405, false},
		{"+3", money.AED, 300, false},
		{"  7.25  ", money.EGP, 725, false},
		{"١٬٢٣٤٫٥٠", money.EGP, 123450, false}, // Arabic-Indic digits + separators
		{"۱۲۳٫۴", money.SAR, 12340, false},     // Extended (Persian) digits
		{".5", money.EGP, 50, false},
		{"92233720368547758.07", money.EGP, math.MaxInt64, false},
		{"-92233720368547758.08", money.EGP, math.MinInt64, false},
		{"92233720368547758.08", money.EGP, 0, true}, // overflow
		{"", money.EGP, 0, true},
		{"-", money.EGP, 0, true},
		{".", money.EGP, 0, true},
		{"1.234", money.EGP, 0, true}, // too many fraction digits
		{"1,23.00", money.EGP, 0, true},
		{"12,34", money.EGP, 0, true},
		{",123", money.EGP, 0, true},
		{"1,,234", money.EGP, 0, true},
		{"1.2.3", money.EGP, 0, true},
		{"1e3", money.EGP, 0, true},
		{"--1", money.EGP, 0, true},
		{"1.", money.EGP, 0, true},
		{"EGP 1", money.EGP, 0, true},
		{"1", money.Currency("XXX"), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := money.Parse(tt.in, tt.cur)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got.Amount() != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.in, got.Amount(), tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		amount int64
		cur    money.Currency
		en, ar string
	}{
		{123450, money.EGP, "EGP 1,234.50", "١٬٢٣٤٫٥٠ ج.م."},
		{5, money.SAR, "SAR 0.05", "٠٫٠٥ ر.س."},
		{-100000, money.AED, "-AED 1,000.00", "-١٬٠٠٠٫٠٠ د.إ."},
		{0, money.EGP, "EGP 0.00", "٠٫٠٠ ج.م."},
		{math.MinInt64, money.EGP, "-EGP 92,233,720,368,547,758.08", "-٩٢٬٢٣٣٬٧٢٠٬٣٦٨٬٥٤٧٬٧٥٨٫٠٨ ج.م."},
	}
	for _, tt := range tests {
		t.Run(tt.en, func(t *testing.T) {
			t.Parallel()
			m := mustNew(t, tt.amount, tt.cur)
			if got := m.Format(money.LocaleEN); got != tt.en {
				t.Errorf("en = %q, want %q", got, tt.en)
			}
			if got := m.Format(money.LocaleAR); got != tt.ar {
				t.Errorf("ar = %q, want %q", got, tt.ar)
			}
			if got := m.String(); got != tt.en {
				t.Errorf("String = %q, want %q", got, tt.en)
			}
		})
	}
}

func TestJSON(t *testing.T) {
	t.Parallel()
	m := mustNew(t, 123450, money.EGP)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"amount":123450,"currency":"EGP"}` {
		t.Fatalf("marshal = %s", b)
	}
	var back money.Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != m {
		t.Fatalf("round trip = %v, want %v", back, m)
	}
	for _, bad := range []string{
		`{"amount":1,"currency":"USD"}`,
		`{"amount":1}`,
		`{"amount":1.5,"currency":"EGP"}`,
		`{"amount":"1","currency":"EGP"}`,
		`[]`,
	} {
		var x money.Money
		if err := json.Unmarshal([]byte(bad), &x); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", bad)
		}
	}
}

func TestCurrencyInfo(t *testing.T) {
	t.Parallel()
	for _, c := range money.Currencies() {
		if !c.Valid() || c.MinorUnits() != 2 {
			t.Errorf("%s: valid=%v minor=%d", c, c.Valid(), c.MinorUnits())
		}
	}
	if money.Currency("XXX").Valid() {
		t.Error("XXX must be invalid")
	}
}
