package web

import (
	"os"
	"strings"
	"testing"
)

func TestSwatches(t *testing.T) {
	t.Parallel()
	tests := []struct{ value, want string }{
		{"كحلي", "sw-navy"},
		{"أبيض", "sw-white"},
		{" ابيض ", "sw-white"},
		{"Navy", "sw-navy"},
		{"رمادي", "sw-gray"},
		{"تركواز غامق", ""}, // unknown: a text button
		{"", ""},
	}
	for _, tt := range tests {
		if got := swatch(tt.value); got != tt.want {
			t.Errorf("swatch(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
	for _, name := range []string{"اللون", "لون", "Color", "الألوان"} {
		if !isColorOption(name) {
			t.Errorf("%q is a color option", name)
		}
	}
	if isColorOption("المقاس") {
		t.Error("size is not a color option")
	}
}

// TestEverySwatchIsPainted: each class the table can return has its rule
// in the stylesheet.
func TestEverySwatchIsPainted(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile("assets/src/site.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range swatches {
		if !strings.Contains(string(css), ".sw-"+c+" ") && !strings.Contains(string(css), ".sw-"+c+",") && !strings.Contains(string(css), ".sw-"+c+"{") {
			t.Errorf("no rule for .sw-%s in site.css", c)
		}
	}
}
