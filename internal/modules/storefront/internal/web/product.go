package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
)

// maxGallery is the most pictures the product page shows, and
// maxRelated the products "you may also like" shows.
const (
	maxGallery = 9
	maxRelated = 4
)

// productView is the product page: the product and everything its
// sections need, decided here so the template only lays it out.
type productView struct {
	pr       catalog.StorefrontProduct
	cats     []catalog.Category // the product's categories, for the line above the title
	current  catalog.StorefrontVariant
	price    priceView
	pct      int    // discount of the current variant
	save     string // "وفّر 60 ج.م"; empty when not on sale
	left     int    // units left to announce (within the store's threshold); 0 none
	options  []optionView
	variants string // the variants as JSON, for the page's script
	money    string // how the script writes amounts
	related  []catalog.StorefrontProduct
	areas    []geo.Area
	quote    bool         // the delivery box can ask for a quote
	refund   *domain.Page // the published refund policy, linked from the page
	whatsApp templ.SafeURL
}

// optionView is one option: its values as buttons or color swatches.
type optionView struct {
	index  int
	name   string
	chosen string
	color  bool
	values []valueView
}

type valueView struct {
	value   string
	swatch  string // sw-<color>, or "" for a text button
	checked bool
	out     bool // no variant with this value can be bought
}

// variantJSON is what the page's script needs of a variant to follow the
// shopper's choice without a request. Left is only the count the store
// announces (0 above its threshold): stock stays private.
type variantJSON struct {
	ID        string   `json:"id"`
	Values    []string `json:"values"`
	Available bool     `json:"available"`
	Amount    int64    `json:"amount"`
	Price     string   `json:"price"`
	Compare   string   `json:"compare,omitempty"`
	Save      string   `json:"save,omitempty"`
	Pct       int      `json:"pct,omitempty"`
	Left      string   `json:"left,omitempty"`
}

func (s *Shop) productView(p *pageCtx, pr catalog.StorefrontProduct, related []catalog.StorefrontProduct) productView {
	threshold := p.v.theme.LowStockThreshold()
	v := productView{pr: pr, current: pr.PriceFrom(), money: p.l.moneyFormat(pr.PriceFrom().Price.Currency()),
		areas: geo.Areas(p.v.country), quote: s.orders != nil}
	v.price, v.pct, v.save = p.priceOf(v.current), salePercent(v.current), p.saving(v.current)
	v.left = shownLeft(v.current, threshold)
	for _, id := range pr.CategoryIDs {
		if c, ok := p.v.categoryByID(id); ok && len(v.cats) < 2 {
			v.cats = append(v.cats, c)
		}
	}
	for i, o := range pr.Options {
		ov := optionView{index: i, name: o.Name, color: isColorOption(o.Name)}
		for _, val := range o.Values {
			vv := valueView{value: val, out: !anyAvailable(pr, i, val)}
			if i < len(v.current.OptionValues) && v.current.OptionValues[i] == val {
				vv.checked, ov.chosen = true, val
			}
			if ov.color {
				vv.swatch = swatch(val)
			}
			ov.values = append(ov.values, vv)
		}
		v.options = append(v.options, ov)
	}
	js := make([]variantJSON, len(pr.Variants))
	for i, pv := range pr.Variants {
		pvw := p.priceOf(pv)
		js[i] = variantJSON{ID: pv.ID.String(), Values: pv.OptionValues, Available: pv.Available, Amount: pv.Price.Amount(),
			Price: pvw.price, Compare: pvw.compare, Save: p.saving(pv), Pct: salePercent(pv)}
		if n := shownLeft(pv, threshold); n > 0 {
			js[i].Left = p.l.Left(n)
		}
		if js[i].Values == nil {
			js[i].Values = []string{}
		}
	}
	b, err := json.Marshal(js)
	if err == nil {
		v.variants = string(b)
	}
	for _, rp := range related {
		if rp.ID != pr.ID && len(v.related) < maxRelated {
			v.related = append(v.related, rp)
		}
	}
	for i := range p.v.policies {
		if pol := p.v.policies[i]; pol.Slug == domain.PolicyRefund {
			v.refund = &pol
		}
	}
	if wa := p.v.theme.Contact.WhatsApp; wa != "" {
		v.whatsApp = whatsAppOrder(p, wa, pr, v.current, 1)
	}
	return v
}

// shownLeft is how many units the page announces: the catalog's count
// (at most catalog.MaxShownLeft) when it is within the store's threshold.
func shownLeft(v catalog.StorefrontVariant, threshold int) int {
	if v.Available && v.Left > 0 && v.Left <= threshold {
		return v.Left
	}
	return 0
}

// anyAvailable reports whether some variant with value at option i can be
// bought.
func anyAvailable(pr catalog.StorefrontProduct, i int, value string) bool {
	for _, v := range pr.Variants {
		if v.Available && i < len(v.OptionValues) && v.OptionValues[i] == value {
			return true
		}
	}
	return false
}

// saving is "وفّر 60 ج.م" for a variant on sale.
func (p *pageCtx) saving(v catalog.StorefrontVariant) string {
	if v.CompareAt == nil || salePercent(v) == 0 {
		return ""
	}
	d, err := v.CompareAt.Sub(v.Price)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(p.l.T(MsgSave), p.l.Price(d))
}

// whatsAppOrder opens a chat with the store, the order typed in: the
// product, its choice, the quantity and the link. The page's script
// rewrites it as the shopper chooses.
func whatsAppOrder(p *pageCtx, number string, pr catalog.StorefrontProduct, v catalog.StorefrontVariant, qty int) templ.SafeURL {
	what := pr.Title
	if len(v.OptionValues) > 0 {
		what += " (" + strings.Join(v.OptionValues, " / ") + ")"
	}
	text := p.l.T(MsgWhatsAppOrder) + " " + what + " × " + fmt.Sprint(qty) + "\n" + p.abs(p.l, productPath(pr.Slug))
	return templ.SafeURL(string(waLink(number)) + "?text=" + url.QueryEscape(text)) //nolint:gosec // our own wa.me link, the text escaped
}
