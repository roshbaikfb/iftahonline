package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestValidateChoice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     domain.Choice
		want   domain.Choice
		fields []string
	}{
		{name: "valid", in: domain.Choice{Activity: "perfume", Look: "soft", Color: "#92400e"},
			want: domain.Choice{Activity: domain.Perfume, Look: domain.Soft, Color: "#92400e"}},
		{name: "colour is lower-cased", in: domain.Choice{Activity: "kids", Look: "classic", Color: " #0E7490 "},
			want: domain.Choice{Activity: domain.Kids, Look: domain.Classic, Color: "#0e7490"}},
		{name: "every field wrong at once", in: domain.Choice{Activity: "cars", Look: "loud", Color: "red"},
			fields: []string{"activity", "look", "color"}},
		{name: "empty", in: domain.Choice{}, fields: []string{"activity", "look", "color"}},
		{name: "short colour", in: domain.Choice{Activity: "food", Look: "modern", Color: "#abc"}, fields: []string{"color"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ValidateChoice(c.in)
			if len(c.fields) == 0 {
				if err != nil || got != c.want {
					t.Fatalf("ValidateChoice(%+v) = %+v, %v; want %+v", c.in, got, err, c.want)
				}
				return
			}
			var ve *errs.Error
			if !errors.As(err, &ve) || ve.Kind != errs.KindValidation {
				t.Fatalf("ValidateChoice(%+v) err = %v; want a validation error", c.in, err)
			}
			if len(ve.Fields) != len(c.fields) {
				t.Fatalf("fields = %+v; want %v", ve.Fields, c.fields)
			}
			for i, f := range c.fields {
				if ve.Fields[i].Field != f {
					t.Fatalf("field %d = %q; want %q", i, ve.Fields[i].Field, f)
				}
			}
		})
	}
}

func TestEveryActivityAndLookIsValid(t *testing.T) {
	t.Parallel()
	if len(domain.Activities()) != 8 || len(domain.Looks()) != 3 {
		t.Fatalf("activities %v, looks %v", domain.Activities(), domain.Looks())
	}
	for _, a := range domain.Activities() {
		for _, l := range domain.Looks() {
			if _, err := domain.ValidateChoice(domain.Choice{Activity: a, Look: l, Color: "#0f2e52"}); err != nil {
				t.Fatalf("%s/%s: %v", a, l, err)
			}
		}
	}
}

func TestStateStatus(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cases := []struct {
		name  string
		state domain.State
		want  domain.Status
	}{
		{name: "a store made before the wizard", state: domain.State{}, want: domain.Completed},
		{name: "a new store", state: domain.State{Started: true}, want: domain.InProgress},
		{name: "a closed wizard", state: domain.State{Started: true, CompletedAt: &now}, want: domain.Completed},
	}
	for _, c := range cases {
		if got := c.state.Status(); got != c.want {
			t.Errorf("%s: Status() = %q; want %q", c.name, got, c.want)
		}
	}
}

func FuzzValidateChoice(f *testing.F) {
	f.Add("clothing", "classic", "#0f2e52")
	f.Add("", "", "")
	f.Add("food", "soft", "#FFFFFF")
	f.Fuzz(func(t *testing.T, activity, look, color string) {
		got, err := domain.ValidateChoice(domain.Choice{Activity: domain.Activity(activity), Look: domain.Look(look), Color: color})
		if err != nil {
			return
		}
		again, err := domain.ValidateChoice(got)
		if err != nil || again != got {
			t.Fatalf("a valid choice must stay valid and unchanged: %+v -> %+v, %v", got, again, err)
		}
		if len(got.Color) != 7 || got.Color[0] != '#' {
			t.Fatalf("color %q", got.Color)
		}
	})
}
