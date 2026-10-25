package domain_test

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func valid(gen *ulid.Generator) domain.NewOrder {
	return domain.NewOrder{
		IdempotencyKey: "checkout-form-0001", Source: domain.SourceStorefront,
		Name: "  أحمد   علي ", Phone: "010 1234 5678", Governorate: "eg-c", City: "مدينة نصر",
		Address: "12 شارع عباس العقاد\nالدور 3", Notes: "",
		Lines: []domain.LineInput{{VariantID: gen.New(), Qty: 2}, {VariantID: gen.New(), Qty: 1}},
	}
}

func fieldCodes(err error) map[string]string {
	out := map[string]string{}
	var e *errs.Error
	if errors.As(err, &e) {
		for _, f := range e.Fields {
			out[f.Field] = f.Code
		}
	}
	return out
}

func TestValidateNewOrder(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	in := valid(gen)
	d, err := domain.ValidateNewOrder(in, geo.EG)
	if err != nil {
		t.Fatal(err)
	}
	c := d.Contact
	if c.Name != "أحمد علي" || c.Phone.Unmasked() != "+201012345678" || c.Governorate != "EG-C" ||
		c.Address != "12 شارع عباس العقاد الدور 3" || d.IdempotencyKey != in.IdempotencyKey {
		t.Fatalf("draft %+v", d)
	}
	if string(d.Lines[0].VariantID[:]) > string(d.Lines[1].VariantID[:]) {
		t.Fatal("lines not sorted by variant")
	}

	// The same order, typed differently, has the same fingerprint; any
	// real change gives another one.
	same := in
	same.Phone, same.Name = "+20 10 1234 5678", "أحمد علي"
	same.Lines = []domain.LineInput{in.Lines[1], in.Lines[0]}
	d2, err := domain.ValidateNewOrder(same, geo.EG)
	if err != nil || d2.Fingerprint != d.Fingerprint || d2.LinesHash != d.LinesHash {
		t.Fatalf("same order, other spelling: %v", err)
	}
	other := in
	other.Notes = "اتصل قبل الوصول"
	d3, _ := domain.ValidateNewOrder(other, geo.EG)
	if d3.Fingerprint == d.Fingerprint || d3.LinesHash != d.LinesHash {
		t.Fatal("notes changed the lines hash or not the fingerprint")
	}
	more := in
	more.Lines = []domain.LineInput{{VariantID: in.Lines[0].VariantID, Qty: 3}, in.Lines[1]}
	d4, _ := domain.ValidateNewOrder(more, geo.EG)
	if d4.LinesHash == d.LinesHash || d4.Fingerprint == d.Fingerprint {
		t.Fatal("quantity change kept the hashes")
	}
	// Field boundaries cannot be moved without changing the fingerprint.
	a, b := in, in
	a.City, a.Address = "ab", "cdefgh"
	b.City, b.Address = "abc", "defgh"
	da, _ := domain.ValidateNewOrder(a, geo.EG)
	db, _ := domain.ValidateNewOrder(b, geo.EG)
	if da.Fingerprint == db.Fingerprint {
		t.Fatal("fingerprint ignores field boundaries")
	}
	// No key is fine (the duplicate window covers it).
	in.IdempotencyKey = ""
	if _, err = domain.ValidateNewOrder(in, geo.EG); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNewOrderRejects(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	v := gen.New()
	tests := []struct {
		name  string
		edit  func(*domain.NewOrder)
		field string
		code  string
	}{
		{"source", func(o *domain.NewOrder) { o.Source = "api" }, "source", "invalid"},
		{"key with spaces", func(o *domain.NewOrder) { o.IdempotencyKey = "a b" }, "idempotency_key", "invalid"},
		{"key too long", func(o *domain.NewOrder) { o.IdempotencyKey = strings.Repeat("k", domain.MaxKeyLen+1) }, "idempotency_key", "too_long"},
		{"no name", func(o *domain.NewOrder) { o.Name = " " }, "name", "required"},
		{"short name", func(o *domain.NewOrder) { o.Name = "A" }, "name", "too_short"},
		{"bad phone", func(o *domain.NewOrder) { o.Phone = "12345" }, "phone", "invalid"},
		{"landline", func(o *domain.NewOrder) { o.Phone = "0223456789" }, "phone", "invalid"},
		{"other country's area", func(o *domain.NewOrder) { o.Governorate = "SA-01" }, "governorate", "invalid"},
		{"no governorate", func(o *domain.NewOrder) { o.Governorate = "" }, "governorate", "invalid"},
		{"no city", func(o *domain.NewOrder) { o.City = "" }, "city", "required"},
		{"short address", func(o *domain.NewOrder) { o.Address = "abc" }, "address", "too_short"},
		{"long notes", func(o *domain.NewOrder) { o.Notes = strings.Repeat("n", domain.MaxNotesLen+1) }, "notes", "too_long"},
		{"no lines", func(o *domain.NewOrder) { o.Lines = nil }, "lines", "count"},
		{"too many lines", func(o *domain.NewOrder) {
			o.Lines = make([]domain.LineInput, domain.MaxLines+1)
			for i := range o.Lines {
				o.Lines[i] = domain.LineInput{VariantID: gen.New(), Qty: 1}
			}
		}, "lines", "count"},
		{"zero qty", func(o *domain.NewOrder) { o.Lines = []domain.LineInput{{VariantID: v, Qty: 0}} }, "lines[0].qty", "range"},
		{"huge qty", func(o *domain.NewOrder) { o.Lines = []domain.LineInput{{VariantID: v, Qty: domain.MaxQty + 1}} }, "lines[0].qty", "range"},
		{"no variant", func(o *domain.NewOrder) { o.Lines = []domain.LineInput{{Qty: 1}} }, "lines[0].variant_id", "required"},
		{"repeated variant", func(o *domain.NewOrder) { o.Lines = []domain.LineInput{{VariantID: v, Qty: 1}, {VariantID: v, Qty: 2}} }, "lines[1].variant_id", "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := valid(gen)
			tt.edit(&in)
			_, err := domain.ValidateNewOrder(in, geo.EG)
			if !errs.Is(err, errs.KindValidation) || fieldCodes(err)[tt.field] != tt.code {
				t.Fatalf("err = %v (fields %v), want %s=%s", err, fieldCodes(err), tt.field, tt.code)
			}
		})
	}
}

func TestCodeAndCreatedAt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 22, 9, 30, 15, 123_000_000, time.UTC)
	gen := ulid.NewGeneratorWith(func() time.Time { return at }, rand.Reader)
	id := gen.New()
	o := domain.Order{ID: id}
	if c := o.Code(); len(c) != 8 || !strings.HasSuffix(id.String(), c) {
		t.Fatalf("code %q of %s", c, id)
	}
	if got := domain.CreatedAtOf(id); !got.Equal(at) {
		t.Fatalf("created at %v, want %v", got, at)
	}
}

func TestResolveQuery(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	cursor := gen.New()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	f, err := domain.ResolveQuery(domain.Query{}, geo.EG)
	if err != nil || f.Limit != domain.DefaultListLimit || f.Before != domain.MaxID || !f.From.Before(f.To) {
		t.Fatalf("defaults %+v %v", f, err)
	}
	f, err = domain.ResolveQuery(domain.Query{Status: ptr(domain.StatusPending), From: &from, To: &to,
		Governorate: "eg-gz", Phone: "01012345678", Before: &cursor, Limit: 10}, geo.EG)
	if err != nil || f.Governorate != "EG-GZ" || f.Phone.Unmasked() != "+201012345678" || f.Before != cursor ||
		!f.From.Equal(from) || f.Limit != 10 {
		t.Fatalf("resolved %+v %v", f, err)
	}
	// The cursor narrows the range to its own millisecond and older.
	if !f.To.Equal(domain.CreatedAtOf(cursor).Add(time.Millisecond)) {
		t.Fatalf("to = %v", f.To)
	}

	tests := []struct {
		name  string
		q     domain.Query
		field string
	}{
		{"limit", domain.Query{Limit: domain.MaxListLimit + 1}, "limit"},
		{"negative limit", domain.Query{Limit: -1}, "limit"},
		{"empty range", domain.Query{From: &to, To: &from}, "to"},
		{"governorate", domain.Query{Governorate: "XX-1"}, "governorate"},
		{"phone", domain.Query{Phone: "123"}, "phone"},
	}
	for _, tt := range tests {
		if _, err := domain.ResolveQuery(tt.q, geo.EG); fieldCodes(err)[tt.field] == "" {
			t.Errorf("%s: err = %v", tt.name, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// FuzzValidateNewOrder: whatever a shopper submits, validation never
// panics, and an accepted draft is clean (one-line text within limits, a
// known governorate, canonical phone).
func FuzzValidateNewOrder(f *testing.F) {
	f.Add("أحمد", "01012345678", "EG-C", "القاهرة", "12 شارع التحرير", "", "key-1", 1)
	f.Add("\x00", "+966501234567", "eg-alx", "x", "\n\n", "\xff", " ", 0)
	f.Add(strings.Repeat("ا", 200), "٠١٠١٢٣٤٥٦٧٨", "EG-", "مدينة", "عنوان طويل", "ملاحظة", "k", 101)
	gen := ulid.NewGenerator()
	v := gen.New()
	f.Fuzz(func(t *testing.T, name, ph, gov, city, addr, notes, key string, qty int) {
		d, err := domain.ValidateNewOrder(domain.NewOrder{IdempotencyKey: key, Source: domain.SourceStorefront, Name: name, Phone: ph,
			Governorate: gov, City: city, Address: addr, Notes: notes, Lines: []domain.LineInput{{VariantID: v, Qty: qty}}}, geo.EG)
		if err != nil {
			if !errs.Is(err, errs.KindValidation) {
				t.Fatalf("unexpected error kind: %v", err)
			}
			return
		}
		c := d.Contact
		for _, s := range []string{c.Name, c.City, c.Address, c.Notes, d.IdempotencyKey} {
			if strings.ContainsAny(s, "\n\r\t\x00") || strings.TrimSpace(s) != s {
				t.Fatalf("unclean text %q", s)
			}
		}
		if _, ok := geo.Lookup(geo.EG, c.Governorate); !ok || !strings.HasPrefix(c.Phone.Unmasked(), "+20") {
			t.Fatalf("draft %+v", c)
		}
	})
}

func TestNormalizeCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"P0R1S2T3", "P0R1S2T3", true},
		{" #p0r1 s2t3 ", "P0R1S2T3", true},
		{"P0R1S2T", "", false},   // short
		{"P0R1S2T34", "", false}, // long
		{"P0R1S2TU", "", false},  // U is not Crockford
		{"P0R1S2T!", "", false},
		{"", "", false},
		{"٠١٢٣٤٥٦٧", "", false}, // Arabic digits are not a code
	}
	for _, tt := range tests {
		got, ok := domain.NormalizeCode(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NormalizeCode(%q) = %q %v, want %q %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	id := ulid.NewGenerator().New()
	if got, ok := domain.NormalizeCode(strings.ToLower(domain.CodeOf(id))); !ok || got != domain.CodeOf(id) {
		t.Fatalf("an order's own code, lower-cased: %q %v", got, ok)
	}
}

// FuzzNormalizeCode: whatever a shopper types, a code that comes out is
// eight Crockford characters.
func FuzzNormalizeCode(f *testing.F) {
	for _, s := range []string{"P0R1S2T3", " #p0r1 s2t3", "\x00\u202e", "IIIIIIII"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		code, ok := domain.NormalizeCode(s)
		if !ok {
			return
		}
		if len(code) != 8 || strings.Trim(code, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" {
			t.Fatalf("code %q from %q", code, s)
		}
	})
}
