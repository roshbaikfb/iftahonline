package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/ads/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

const pub = "ca-pub-1234567890123456"

func TestSettingsValidate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		s  domain.Settings
		ok bool
	}{
		"off":                {domain.Settings{}, true},
		"on with publisher":  {domain.Settings{AdSenseEnabled: true, PublisherID: pub, Units: map[domain.Placement]string{domain.Grid: "1234567890"}}, true},
		"on, no publisher":   {domain.Settings{AdSenseEnabled: true}, false},
		"bad publisher":      {domain.Settings{PublisherID: "pub-1"}, false},
		"short unit":         {domain.Settings{Units: map[domain.Placement]string{domain.Grid: "123"}}, false},
		"empty unit":         {domain.Settings{Units: map[domain.Placement]string{domain.Grid: ""}}, true},
		"unknown placement":  {domain.Settings{Units: map[domain.Placement]string{"footer": "1234567890"}}, false},
		"off keeps a pubkey": {domain.Settings{PublisherID: pub}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.s.Validate()
			if tc.ok != (err == nil) || (err != nil && !errs.Is(err, errs.KindValidation)) {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestAdsTxt(t *testing.T) {
	t.Parallel()
	if got := (domain.Settings{AdSenseEnabled: true, PublisherID: pub}).AdsTxt(); got != "google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0\n" {
		t.Fatalf("on = %q", got)
	}
	if got := (domain.Settings{PublisherID: pub}).AdsTxt(); got != "" {
		t.Fatalf("off = %q", got)
	}
}

func TestBannerValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	good := domain.Banner{Title: "افتح متجرك", Link: "https://iftahonline.com/start", Placement: domain.HomeTop, StartsAt: now, Weight: 10}
	for name, tc := range map[string]struct {
		edit  func(*domain.Banner)
		field string
	}{
		"good":          {func(*domain.Banner) {}, ""},
		"every country": {func(b *domain.Banner) { b.Country = "" }, ""},
		"egypt":         {func(b *domain.Banner) { b.Country = "EG" }, ""},
		"no title":      {func(b *domain.Banner) { b.Title = "  " }, "title"},
		"http link":     {func(b *domain.Banner) { b.Link = "http://x.com" }, "link_url"},
		"script link":   {func(b *domain.Banner) { b.Link = "javascript:alert(1)" }, "link_url"},
		"placement":     {func(b *domain.Banner) { b.Placement = "cart" }, "placement"},
		"country":       {func(b *domain.Banner) { b.Country = "US" }, "country"},
		"no start":      {func(b *domain.Banner) { b.StartsAt = time.Time{} }, "ends_at"},
		"ends first":    {func(b *domain.Banner) { b.EndsAt = now.Add(-time.Hour) }, "ends_at"},
		"weight 0":      {func(b *domain.Banner) { b.Weight = 0 }, "weight"},
		"weight 101":    {func(b *domain.Banner) { b.Weight = 101 }, "weight"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := good
			tc.edit(&b)
			err := b.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var fe []errs.FieldError
			if e := (*errs.Error)(nil); errors.As(err, &e) {
				fe = e.Fields
			}
			if len(fe) != 1 || fe[0].Field != tc.field {
				t.Fatalf("Validate() = %v, want %s", err, tc.field)
			}
		})
	}
}

func TestPlacements(t *testing.T) {
	t.Parallel()
	for _, p := range domain.Placements() {
		if !p.Valid() {
			t.Fatalf("%s not valid", p)
		}
	}
	if w, h := domain.HomeTop.Size(); w != 320 || h != 100 {
		t.Fatalf("home_top = %dx%d", w, h)
	}
	if w, h := domain.Grid.Size(); w != 300 || h != 250 {
		t.Fatalf("grid = %dx%d", w, h)
	}
	id := id("01J9ZQ3W5Y8V4K2N6M7P0R1S2T")
	if k := domain.ImageKey(id, "png"); k != "platform/ads/01J9ZQ3W5Y8V4K2N6M7P0R1S2T.png" {
		t.Fatalf("ImageKey = %s", k)
	}
}

func TestChoose(t *testing.T) {
	t.Parallel()
	a := domain.Banner{ID: id("01J9ZQ3W5Y8V4K2N6M7P0R1S2A"), Placement: domain.HomeTop, Weight: 1}
	b := domain.Banner{ID: id("01J9ZQ3W5Y8V4K2N6M7P0R1S2B"), Placement: domain.HomeTop, Weight: 3}
	on := domain.Settings{AdSenseEnabled: true, PublisherID: pub, Units: map[domain.Placement]string{domain.HomeTop: "1111111111", domain.Grid: "2222222222"}}
	for name, tc := range map[string]struct {
		s      domain.Settings
		live   []domain.Banner
		draw   int
		banner map[domain.Placement]ulid.ID
		unit   map[domain.Placement]string
	}{
		"nothing":               {domain.Settings{}, nil, 0, nil, nil},
		"adsense where a unit":  {on, nil, 0, nil, map[domain.Placement]string{domain.HomeTop: "1111111111", domain.Grid: "2222222222"}},
		"adsense off":           {domain.Settings{Units: on.Units, PublisherID: pub}, nil, 0, nil, nil},
		"banner first, by draw": {on, []domain.Banner{a, b}, 0, map[domain.Placement]ulid.ID{domain.HomeTop: a.ID}, map[domain.Placement]string{domain.Grid: "2222222222"}},
		"heavier banner":        {on, []domain.Banner{a, b}, 1, map[domain.Placement]ulid.ID{domain.HomeTop: b.ID}, map[domain.Placement]string{domain.Grid: "2222222222"}},
		"last of the weights":   {domain.Settings{}, []domain.Banner{a, b}, 3, map[domain.Placement]ulid.ID{domain.HomeTop: b.ID}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := domain.Choose(tc.s, tc.live, func(n int) int {
				if tc.draw >= n {
					t.Fatalf("draw %d of %d", tc.draw, n)
				}
				return tc.draw
			})
			if len(got) != len(domain.Placements()) {
				t.Fatalf("slots = %v", got)
			}
			for _, p := range domain.Placements() {
				sl := got[p]
				if id, want := tc.banner[p]; want != (sl.Banner != nil) || (want && sl.Banner.ID != id) {
					t.Fatalf("%s banner = %+v, want %v", p, sl.Banner, id)
				}
				if sl.Unit != tc.unit[p] {
					t.Fatalf("%s unit = %q, want %q", p, sl.Unit, tc.unit[p])
				}
				if sl.Empty() != (sl.Banner == nil && tc.unit[p] == "") {
					t.Fatalf("%s empty = %v", p, sl.Empty())
				}
			}
		})
	}
}

func id(s string) ulid.ID {
	v, err := ulid.Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}
