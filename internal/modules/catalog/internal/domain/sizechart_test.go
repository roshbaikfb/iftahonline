package domain_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
)

func validChart() domain.SizeChartInput {
	return domain.SizeChartInput{
		Name:   " تيشيرتات رجالي ",
		Header: []string{" المقاس", "الصدر", "الطول "},
		Body:   [][]string{{"S", " 96", "68"}, {"M", "102", ""}},
	}
}

func TestValidateSizeChartNormalizes(t *testing.T) {
	t.Parallel()
	out, err := domain.ValidateSizeChart(validChart())
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "تيشيرتات رجالي" || !slices.Equal(out.Header, []string{"المقاس", "الصدر", "الطول"}) {
		t.Fatalf("normalized = %q %q", out.Name, out.Header)
	}
	if !slices.Equal(out.Body[0], []string{"S", "96", "68"}) || out.Body[1][2] != "" {
		t.Fatalf("body = %q (a measurement may be blank)", out.Body)
	}
}

func TestValidateSizeChartRules(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", domain.MaxSizeChartCellLen+1)
	tests := []struct {
		name   string
		mutate func(*domain.SizeChartInput)
		want   []string
	}{
		{"empty name", func(c *domain.SizeChartInput) { c.Name = " " }, []string{"name:length"}},
		{"long name", func(c *domain.SizeChartInput) { c.Name = strings.Repeat("n", domain.MaxSizeChartNameLen+1) }, []string{"name:length"}},
		{"one column", func(c *domain.SizeChartInput) {
			c.Header = []string{"المقاس"}
			c.Body = [][]string{{"S"}}
		}, []string{"header:count"}},
		{"too many columns", func(c *domain.SizeChartInput) {
			c.Header = make([]string, domain.MaxSizeChartColumns+1)
		}, []string{"header:count"}},
		{"blank column name", func(c *domain.SizeChartInput) { c.Header[1] = " " }, []string{"header[1]:length"}},
		{"long column name", func(c *domain.SizeChartInput) { c.Header[2] = long }, []string{"header[2]:length"}},
		{"control char", func(c *domain.SizeChartInput) { c.Header[0] = "a\x00b" }, []string{"header[0]:invalid"}},
		{"no rows", func(c *domain.SizeChartInput) { c.Body = nil }, []string{"body:count"}},
		{"too many rows", func(c *domain.SizeChartInput) {
			c.Body = make([][]string, domain.MaxSizeChartRows+1)
		}, []string{"body:count"}},
		{"short row", func(c *domain.SizeChartInput) { c.Body[1] = []string{"M", "102"} }, []string{"body[1]:count"}},
		{"long row", func(c *domain.SizeChartInput) { c.Body[0] = append(c.Body[0], "1") }, []string{"body[0]:count"}},
		{"no size", func(c *domain.SizeChartInput) { c.Body[1][0] = " " }, []string{"body[1][0]:length"}},
		{"long cell", func(c *domain.SizeChartInput) { c.Body[0][1] = long }, []string{"body[0][1]:length"}},
		{"several at once", func(c *domain.SizeChartInput) {
			c.Name = ""
			c.Body[0][0] = ""
		}, []string{"name:length", "body[0][0]:length"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := validChart()
			tt.mutate(&in)
			_, err := domain.ValidateSizeChart(in)
			if got := fieldCodes(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateProductColors(t *testing.T) {
	t.Parallel()
	in := validInput()
	in.Options[1].Colors = []string{" #1F2A44 "}
	out, err := domain.ValidateProduct(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Options[1].Colors, []string{"#1f2a44"}) || out.Options[0].Colors != nil {
		t.Fatalf("colors = %q / %q", out.Options[1].Colors, out.Options[0].Colors)
	}
	in.Options[1].Colors = []string{""} // guess from the name
	if _, err = domain.ValidateProduct(in); err != nil {
		t.Fatalf("an empty colour is a guess: %v", err)
	}

	tests := []struct {
		name   string
		colors []string
		want   []string
	}{
		{"fewer than values", []string{}, []string{"options[1].colors:count"}},
		{"more than values", []string{"#000000", "#ffffff"}, []string{"options[1].colors:count"}},
		{"named colour", []string{"red"}, []string{"options[1].colors[0]:invalid"}},
		{"short hex", []string{"#fff"}, []string{"options[1].colors[0]:invalid"}},
		{"css injection", []string{"#000;}x"}, []string{"options[1].colors[0]:invalid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := validInput()
			in.Options[1].Colors = tt.colors
			_, err := domain.ValidateProduct(in)
			if got := fieldCodes(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsHexColor(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"#1f2a44": true, "#000000": true, "#1F2A44": false, "1f2a44": false, "#1f2a4": false,
		"#1f2a444": false, "#gggggg": false, "": false, "#00000\x00": false,
	} {
		if got := domain.IsHexColor(s); got != want {
			t.Errorf("IsHexColor(%q) = %v, want %v", s, got, want)
		}
	}
}

// FuzzValidateSizeChart feeds arbitrary merchant text through the chart
// rules: they never panic, and an accepted chart is always well formed.
func FuzzValidateSizeChart(f *testing.F) {
	f.Add("تيشيرتات", "المقاس|الصدر", "S|96\nM|102")
	f.Add(" ", "a", "")
	f.Add("x", "a|b|c", "1|2\n|3|4")
	f.Fuzz(func(t *testing.T, name, header, body string) {
		in := domain.SizeChartInput{Name: name, Header: strings.Split(header, "|")}
		for line := range strings.SplitSeq(body, "\n") {
			in.Body = append(in.Body, strings.Split(line, "|"))
		}
		out, err := domain.ValidateSizeChart(in)
		if err != nil {
			return
		}
		if len(out.Header) < domain.MinSizeChartColumns || len(out.Header) > domain.MaxSizeChartColumns ||
			len(out.Body) == 0 || len(out.Body) > domain.MaxSizeChartRows {
			t.Fatalf("accepted a chart of %d columns and %d rows", len(out.Header), len(out.Body))
		}
		for _, row := range out.Body {
			if len(row) != len(out.Header) || row[0] == "" {
				t.Fatalf("accepted row %q for header %q", row, out.Header)
			}
		}
	})
}
