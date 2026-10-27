package web_test

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
)

// TestMerchantColors (ADR-0035): a colour the merchant set paints its
// swatch through the page's own stylesheet, which the CSP allows by hash;
// a value without one still guesses from its name, and an option marked
// as colours shows swatches whatever its name.
func TestMerchantColors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := product("بولو", "polo", 32000, true, f.cat.ID)
	tee.Options = []catalog.Option{
		{Name: "المقاس", Values: []string{"M"}},
		{Name: "Shade", Values: []string{"ليلي", "أبيض", "رملي"}, Colors: []string{"#1f2a44", "", "bad;}"}},
	}
	tee.Variants = []catalog.StorefrontVariant{
		{ID: gen.New(), OptionValues: []string{"M", "ليلي"}, Price: egp(32000), Available: true},
		{ID: gen.New(), OptionValues: []string{"M", "أبيض"}, Price: egp(32000), Available: true},
		{ID: gen.New(), OptionValues: []string{"M", "رملي"}, Price: egp(32000), Available: true},
	}
	f.catalog.products = append(f.catalog.products, tee)
	res := f.get(t, "/p/polo")
	body := res.Body.String()
	css := ".swc-1-0{background:#1f2a44}"
	wantContains(t, body,
		`<fieldset class="opt colors"><legend>Shade:`,
		`<label class="sw swc-1-0"><input type="radio" name="o1" value="ليلي" checked`,
		`<label class="sw sw-white"><input type="radio" name="o1" value="أبيض"`,
		// a colour that is not "#rrggbb" never reaches the stylesheet
		`<label class=""><input type="radio" name="o1" value="رملي"`, `<span>رملي</span>`,
		"<style>"+css+"</style>",
	)
	if strings.Contains(body, "bad;}") && strings.Contains(body, "background:bad") {
		t.Fatal("an invalid colour reached the stylesheet")
	}
	sum := sha256.Sum256([]byte(css))
	policy := res.Header().Get("Content-Security-Policy")
	if want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !strings.Contains(policy, want) || strings.Count(policy, "'sha256-") != 2 {
		t.Fatalf("CSP %q does not allow exactly the store's and the page's style (%s)", policy, want)
	}

	// a page without merchant colours keeps one style and one hash
	plain := f.get(t, "/p/cotton-tee")
	if p := plain.Header().Get("Content-Security-Policy"); strings.Count(p, "'sha256-") != 1 || strings.Count(plain.Body.String(), "<style>") != 1 {
		t.Fatalf("plain page CSP %q", p)
	}
}

// TestSizeChart: the product's size chart opens under its title, a
// table in centimetres with the size as each row's header; a product
// without one shows none.
func TestSizeChart(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.catalog.products[0].SizeChart = &catalog.StorefrontSizeChart{
		Header: []string{"المقاس", "الصدر", "الطول"},
		Body:   [][]string{{"S", "96", "68"}, {"M", "102", ""}},
	}
	body := f.get(t, "/p/cotton-tee").Body.String()
	wantContains(t, body,
		`<details class="pd-desc pd-size"><summary>جدول المقاسات</summary>`,
		`<caption>القياسات بالسنتيمتر</caption>`,
		`<th scope="col">المقاس</th><th scope="col">الصدر</th><th scope="col">الطول</th>`,
		`<tr><th scope="row">S</th><td>96</td><td>68</td></tr>`,
		`<tr><th scope="row">M</th><td>102</td><td></td></tr>`,
	)

	f2 := newFixture(t)
	if strings.Contains(f2.get(t, "/p/cotton-tee").Body.String(), `class="pd-desc pd-size"`) {
		t.Fatal("a product without a size chart shows one")
	}
}
