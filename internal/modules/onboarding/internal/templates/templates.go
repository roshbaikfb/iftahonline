// Package templates holds the setup wizard's starter templates
// (ADR-0034): one versioned data file per activity, with its categories,
// sample products and their pictures, and the code that applies one to a
// store through the catalog and storefront modules' Starter interfaces.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

//go:embed data/*.json images/*.jpg
var files embed.FS

// Template is one activity's starter content.
type Template struct {
	Version      int        `json:"version"`
	Activity     string     `json:"activity"`
	Accent       string     `json:"accent"` // the theme's accent (#rrggbb)
	Announcement string     `json:"announcement"`
	Hero         Hero       `json:"hero"`
	Categories   []Category `json:"categories"`
	Products     []Product  `json:"products"`
}

// Hero is the home page's opening text.
type Hero struct {
	Heading    string `json:"heading"`
	Subheading string `json:"subheading"`
}

// Category is a starter category and the picture its products share.
type Category struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Image string `json:"image"` // a file in images/, without .jpg
}

// Product is a sample product. With an option, it has one variant per
// value, all at the same price.
type Product struct {
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Category    string           `json:"category"` // a category's slug
	Option      *Option          `json:"option,omitempty"`
	Prices      map[string]int64 `json:"prices"` // minor units, per currency
	Stock       int              `json:"stock"`
}

// Option is a product dimension (size, age) and its values.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Parse reads one template file strictly: unknown fields, trailing data
// and broken references are errors.
func Parse(data []byte) (Template, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, fmt.Errorf("templates: %w", err)
	}
	if dec.More() {
		return Template{}, errors.New("templates: trailing data")
	}
	return t, t.validate()
}

func (t Template) validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if t.Version < 1 {
		add("version must be 1 or more")
	}
	if !slices.Contains(domain.Activities(), domain.Activity(t.Activity)) {
		add("unknown activity %q", t.Activity)
	}
	if !hex(t.Accent) {
		add("accent %q is not #rrggbb", t.Accent)
	}
	if strings.TrimSpace(t.Hero.Heading) == "" || len(t.Categories) == 0 || len(t.Products) == 0 {
		add("a hero heading, categories and products are required")
	}
	cats := map[string]bool{}
	for _, c := range t.Categories {
		if c.Slug == "" || c.Name == "" || cats[c.Slug] {
			add("category %q: a unique slug and a name are required", c.Slug)
		}
		cats[c.Slug] = true
		if _, err := fs.Stat(files, "images/"+c.Image+".jpg"); err != nil {
			add("category %q: no picture %q", c.Slug, c.Image)
		}
	}
	slugs := map[string]bool{}
	for _, p := range t.Products {
		if !strings.HasPrefix(p.Slug, "sample-") || slugs[p.Slug] || p.Title == "" {
			add("product %q: a unique sample- slug and a title are required", p.Slug)
		}
		slugs[p.Slug] = true
		if !cats[p.Category] {
			add("product %q: unknown category %q", p.Slug, p.Category)
		}
		for _, c := range money.Currencies() {
			if p.Prices[string(c)] <= 0 {
				add("product %q: no %s price", p.Slug, c)
			}
		}
		if p.Stock < 1 {
			add("product %q: stock must be 1 or more", p.Slug)
		}
		if p.Option != nil && (p.Option.Name == "" || len(p.Option.Values) == 0) {
			add("product %q: an option needs a name and values", p.Slug)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("templates: %s: %s", t.Activity, strings.Join(problems, "; "))
	}
	return nil
}

func hex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Load reads every template, one per activity.
func Load() (map[domain.Activity]Template, error) {
	out := make(map[domain.Activity]Template, len(domain.Activities()))
	for _, a := range domain.Activities() {
		data, err := files.ReadFile("data/" + string(a) + ".json")
		if err != nil {
			return nil, fmt.Errorf("templates: %s: %w", a, err)
		}
		t, err := Parse(data)
		if err != nil {
			return nil, err
		}
		if t.Activity != string(a) {
			return nil, fmt.Errorf("templates: %s.json is for %q", a, t.Activity)
		}
		out[a] = t
	}
	return out, nil
}

// picture returns a category's picture.
func picture(name string) ([]byte, error) {
	b, err := files.ReadFile("images/" + name + ".jpg")
	if err != nil {
		return nil, fmt.Errorf("templates: picture %s: %w", name, err)
	}
	return b, nil
}
