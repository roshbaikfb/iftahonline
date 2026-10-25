package web

import (
	"net/url"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func shirtForChoice() catalog.StorefrontProduct {
	gen := ulid.NewGenerator()
	return catalog.StorefrontProduct{
		ID:      gen.New(),
		Options: []catalog.Option{{Name: "المقاس", Values: []string{"M", "L"}}, {Name: "اللون", Values: []string{"كحلي", "أبيض"}}},
		Variants: []catalog.StorefrontVariant{
			{ID: gen.New(), OptionValues: []string{"M", "كحلي"}},
			{ID: gen.New(), OptionValues: []string{"M", "أبيض"}},
			{ID: gen.New(), OptionValues: []string{"L", "كحلي"}},
		},
	}
}

func TestPickVariant(t *testing.T) {
	t.Parallel()
	pr := shirtForChoice()
	tests := []struct {
		name   string
		values []string
		want   int // index in pr.Variants; -1 none
	}{
		{"exact", []string{"M", "أبيض"}, 1},
		{"other", []string{"L", "كحلي"}, 2},
		{"a combination that does not exist", []string{"L", "أبيض"}, -1},
		{"too few values", []string{"M"}, -1},
		{"too many values", []string{"M", "كحلي", "x"}, -1},
		{"no values", nil, -1},
		{"case and spaces are not guessed", []string{"m", "كحلي "}, -1},
	}
	for _, tt := range tests {
		id, ok := pickVariant(pr, tt.values)
		switch {
		case tt.want < 0 && ok:
			t.Errorf("%s: picked %v", tt.name, id)
		case tt.want >= 0 && (!ok || id != pr.Variants[tt.want].ID):
			t.Errorf("%s: picked %v %v, want variant %d", tt.name, id, ok, tt.want)
		}
	}
}

func TestChoiceFromForm(t *testing.T) {
	t.Parallel()
	pid := ulid.NewGenerator().New()
	tests := []struct {
		name string
		form url.Values
		ok   bool
		n    int
	}{
		{"product and two values", url.Values{"product": {pid.String()}, "o0": {"M"}, "o1": {"كحلي"}}, true, 2},
		{"product without options", url.Values{"product": {pid.String()}}, true, 0},
		{"a gap in the values", url.Values{"product": {pid.String()}, "o0": {"M"}, "o2": {"x"}}, false, 0},
		{"no product", url.Values{"o0": {"M"}}, false, 0},
		{"bad product", url.Values{"product": {"nope"}, "o0": {"M"}}, false, 0},
		{"value too long", url.Values{"product": {pid.String()}, "o0": {string(make([]byte, maxChoiceLen+1))}}, false, 0},
		{"repeated value", url.Values{"product": {pid.String()}, "o0": {"M", "L"}}, false, 0},
	}
	for _, tt := range tests {
		got, values, ok := choiceFromForm(tt.form)
		if ok != tt.ok || (ok && (got != pid || len(values) != tt.n)) {
			t.Errorf("%s: %v %q %v", tt.name, got, values, ok)
		}
	}
}

// FuzzChoice: whatever a form says, reading it and picking a variant
// never panics, and a pick is always one of the product's variants whose
// values are exactly the ones sent.
func FuzzChoice(f *testing.F) {
	pr := shirtForChoice()
	f.Add(pr.ID.String(), "M", "كحلي", "")
	f.Add(pr.ID.String(), "L", "أبيض", "x")
	f.Add("nope", "", "", "")
	f.Add(pr.ID.String(), "\x00", "‮", "M")
	f.Fuzz(func(t *testing.T, product, o0, o1, o2 string) {
		form := url.Values{"product": {product}}
		for i, v := range []string{o0, o1, o2} {
			if v != "" {
				form.Set("o"+string(rune('0'+i)), v)
			}
		}
		_, values, ok := choiceFromForm(form)
		if !ok {
			return
		}
		id, picked := pickVariant(pr, values)
		if !picked {
			return
		}
		for _, v := range pr.Variants {
			if v.ID == id {
				if len(v.OptionValues) != len(values) {
					t.Fatalf("picked %v for %q", v.OptionValues, values)
				}
				for i := range values {
					if v.OptionValues[i] != values[i] {
						t.Fatalf("picked %v for %q", v.OptionValues, values)
					}
				}
				return
			}
		}
		t.Fatalf("picked %v, not a variant of the product", id)
	})
}
