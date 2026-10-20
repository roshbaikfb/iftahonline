package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
)

// WooConfig connects to a WooCommerce store's REST API (v3).
type WooConfig struct {
	BaseURL string // the shop's URL, e.g. https://shop.example.com
	Key     string // consumer key (ck_…)
	Secret  string // consumer secret (cs_…)
	// Client must be a platform/safehttp client: the URL is the merchant's.
	Client   *http.Client
	Currency money.Currency
	PerPage  int // default 50
	Retry    retry.Policy
	// AllowHTTP permits http:// (tests only): basic auth over plain HTTP
	// would leak the merchant's keys.
	AllowHTTP bool
}

// Woo is a Source over a WooCommerce catalog, oldest product first.
type Woo struct {
	cfg   WooConfig
	base  *url.URL
	total int
	pages int
	page  int
	last  int // products on the last page fetched
	buf   []wooProduct
	pos   int // records consumed
}

type wooProduct struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	Type             string         `json:"type"`
	Status           string         `json:"status"`
	Description      string         `json:"description"`
	ShortDescription string         `json:"short_description"`
	SKU              string         `json:"sku"`
	RegularPrice     string         `json:"regular_price"`
	SalePrice        string         `json:"sale_price"`
	ManageStock      bool           `json:"manage_stock"`
	StockQuantity    *int           `json:"stock_quantity"`
	Categories       []wooCategory  `json:"categories"`
	Attributes       []wooAttribute `json:"attributes"`
}

type wooCategory struct {
	Name string `json:"name"`
}

type wooAttribute struct {
	Name      string   `json:"name"`
	Option    string   `json:"option"`  // on a variation
	Options   []string `json:"options"` // on a product
	Variation bool     `json:"variation"`
}

type wooVariation struct {
	SKU           string          `json:"sku"`
	RegularPrice  string          `json:"regular_price"`
	SalePrice     string          `json:"sale_price"`
	ManageStock   json.RawMessage `json:"manage_stock"` // true, false or "parent"
	StockQuantity *int            `json:"stock_quantity"`
	Attributes    []wooAttribute  `json:"attributes"`
}

// OpenWoo checks the credentials and reads the catalog size.
func OpenWoo(ctx context.Context, cfg WooConfig) (*Woo, error) {
	if cfg.PerPage <= 0 {
		cfg.PerPage = 50
	}
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry = retry.Default()
	}
	u, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && (!cfg.AllowHTTP || u.Scheme != "http")) {
		return nil, sourceErr("woo_bad_url", "the shop URL must start with https://")
	}
	w := &Woo{cfg: cfg, base: u}
	if err := w.fetchPage(ctx, 1); err != nil {
		return nil, err
	}
	return w, nil
}

// Total implements Source.
func (w *Woo) Total() int { return w.total }

func (w *Woo) endpoint(path string, q url.Values) string {
	u := *w.base
	u.Path = strings.TrimRight(u.Path, "/") + "/wp-json/wc/v3/" + path
	u.RawQuery = q.Encode()
	return u.String()
}

// get fetches one JSON document, retrying transient failures.
func (w *Woo) get(ctx context.Context, endpoint string, out any) (http.Header, error) {
	var hdr http.Header
	err := retry.Do(ctx, w.cfg.Retry, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return retry.Permanent(err)
		}
		req.SetBasicAuth(w.cfg.Key, w.cfg.Secret)
		req.Header.Set("Accept", "application/json")
		res, err := w.cfg.Client.Do(req)
		if errors.Is(err, safehttp.ErrBlockedAddress) || errors.Is(err, safehttp.ErrBlockedPort) {
			return retry.Permanent(sourceErr("woo_blocked_address", "the shop URL points to a private or reserved address"))
		}
		if err != nil {
			return err // network: retry, then the job retries
		}
		defer func() { _ = res.Body.Close() }()
		switch {
		case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
			return retry.Permanent(sourceErr("woo_auth", "WooCommerce refused the API keys (HTTP %d)", res.StatusCode))
		case res.StatusCode == http.StatusNotFound:
			return retry.Permanent(sourceErr("woo_not_found", "no WooCommerce REST API at this URL"))
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				return fmt.Errorf("importer: woocommerce: HTTP %d: %w", res.StatusCode, err)
			}
			return fmt.Errorf("importer: woocommerce: HTTP %d", res.StatusCode)
		case res.StatusCode != http.StatusOK:
			return retry.Permanent(sourceErr("woo_bad_response", "unexpected HTTP %d", res.StatusCode))
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return retry.Permanent(sourceErr("woo_bad_response", "the response is not WooCommerce JSON"))
		}
		hdr = res.Header
		return nil
	})
	return hdr, err
}

func (w *Woo) fetchPage(ctx context.Context, page int) error {
	q := url.Values{"per_page": {strconv.Itoa(w.cfg.PerPage)}, "page": {strconv.Itoa(page)},
		"orderby": {"id"}, "order": {"asc"}, "status": {"any"}}
	var products []wooProduct
	hdr, err := w.get(ctx, w.endpoint("products", q), &products)
	if err != nil {
		return err
	}
	if page == 1 {
		w.total = atoiOr(hdr.Get("X-WP-Total"), -1)
		w.pages = atoiOr(hdr.Get("X-WP-TotalPages"), 0)
		if w.total > domain.MaxImportRecords {
			return sourceErr("too_many_products", "at most %d products per import", domain.MaxImportRecords)
		}
	}
	w.page, w.buf, w.last = page, products, len(products)
	return nil
}

// take returns the next raw product, fetching pages as needed.
func (w *Woo) take(ctx context.Context) (wooProduct, int, error) {
	for len(w.buf) == 0 {
		// Without X-WP-TotalPages, a short page is the last one.
		if (w.pages > 0 && w.page >= w.pages) || (w.pages == 0 && w.last < w.cfg.PerPage) {
			return wooProduct{}, 0, io.EOF
		}
		if err := w.fetchPage(ctx, w.page+1); err != nil {
			return wooProduct{}, 0, err
		}
		if len(w.buf) == 0 {
			return wooProduct{}, 0, io.EOF
		}
	}
	p := w.buf[0]
	w.buf = w.buf[1:]
	w.pos++
	return p, w.pos, nil
}

// Skip implements Source.
func (w *Woo) Skip(ctx context.Context) error {
	_, _, err := w.take(ctx)
	return err
}

// Next implements Source.
func (w *Woo) Next(ctx context.Context) (domain.ImportRecord, error) {
	p, pos, err := w.take(ctx)
	if err != nil {
		return domain.ImportRecord{}, err
	}
	rec, err := w.record(ctx, p)
	if err != nil {
		if _, fatal := IsSourceError(err); fatal {
			return domain.ImportRecord{}, err
		}
		var rowErr rowError
		if errors.As(err, &rowErr) {
			return domain.ImportRecord{Line: pos, Err: fmt.Sprintf("%s: %s", html.UnescapeString(p.Name), rowErr.msg)}, nil
		}
		return domain.ImportRecord{}, err
	}
	rec.Line = pos
	return rec, nil
}

// rowError is a product WooCommerce returned that we cannot represent.
type rowError struct{ msg string }

func (e rowError) Error() string { return e.msg }

func (w *Woo) record(ctx context.Context, p wooProduct) (domain.ImportRecord, error) {
	in := domain.ProductInput{
		Slug: p.Slug, Title: html.UnescapeString(p.Name), Description: StripHTML(p.Description),
		Status: string(domain.StatusDraft),
	}
	if in.Description == "" {
		in.Description = StripHTML(p.ShortDescription)
	}
	if p.Status == "publish" {
		in.Status = string(domain.StatusActive)
	}
	var cats []string
	for _, c := range p.Categories {
		if name := html.UnescapeString(c.Name); name != "" && !strings.EqualFold(name, "uncategorized") {
			cats = append(cats, name)
		}
	}
	switch p.Type {
	case "simple", "":
		v, err := w.variant(p.SKU, p.RegularPrice, p.SalePrice, p.ManageStock, p.StockQuantity)
		if err != nil {
			return domain.ImportRecord{}, err
		}
		in.Variants = []domain.VariantInput{v}
	case "variable":
		if err := w.variable(ctx, p, &in); err != nil {
			return domain.ImportRecord{}, err
		}
	default:
		return domain.ImportRecord{}, rowError{msg: fmt.Sprintf("%q products are not supported", p.Type)}
	}
	return domain.ImportRecord{Product: in, Categories: cats}, nil
}

func (w *Woo) variable(ctx context.Context, p wooProduct, in *domain.ProductInput) error {
	for _, a := range p.Attributes {
		if a.Variation {
			values := make([]string, len(a.Options))
			for i, o := range a.Options {
				values[i] = html.UnescapeString(o)
			}
			in.Options = append(in.Options, domain.Option{Name: html.UnescapeString(a.Name), Values: values})
		}
	}
	var vars []wooVariation
	q := url.Values{"per_page": {"100"}, "orderby": {"id"}, "order": {"asc"}}
	if _, err := w.get(ctx, w.endpoint("products/"+strconv.FormatInt(p.ID, 10)+"/variations", q), &vars); err != nil {
		return err
	}
	if len(vars) == 0 {
		return rowError{msg: "a variable product without variations"}
	}
	for _, v := range vars {
		values := make([]string, len(in.Options))
		for _, a := range v.Attributes {
			for i, o := range in.Options {
				if strings.EqualFold(o.Name, html.UnescapeString(a.Name)) {
					values[i] = html.UnescapeString(a.Option)
				}
			}
		}
		for _, val := range values {
			if val == "" {
				return rowError{msg: `variations set to "any" value are not supported`}
			}
		}
		manage := strings.TrimSpace(string(v.ManageStock)) == "true"
		dv, err := w.variant(v.SKU, v.RegularPrice, v.SalePrice, manage, v.StockQuantity)
		if err != nil {
			return err
		}
		dv.OptionValues = values
		in.Variants = append(in.Variants, dv)
	}
	return nil
}

func (w *Woo) variant(sku, regular, sale string, manage bool, qty *int) (domain.VariantInput, error) {
	v := domain.VariantInput{SKU: sku, Tracked: &manage}
	if manage && qty != nil {
		v.OnHand = max(*qty, 0)
	}
	if strings.TrimSpace(regular) == "" {
		return v, rowError{msg: "no regular price"}
	}
	reg, err := money.Parse(regular, w.cfg.Currency)
	if err != nil {
		return v, rowError{msg: fmt.Sprintf("price %q is not an amount in %s", regular, w.cfg.Currency)}
	}
	v.PriceMinor = reg.Amount()
	if strings.TrimSpace(sale) != "" {
		s, err := money.Parse(sale, w.cfg.Currency)
		if err != nil {
			return v, rowError{msg: fmt.Sprintf("sale price %q is not an amount in %s", sale, w.cfg.Currency)}
		}
		if s.Amount() < reg.Amount() {
			compare := reg.Amount()
			v.PriceMinor, v.CompareAtMinor = s.Amount(), &compare
		}
	}
	return v, nil
}

// StripHTML turns WooCommerce's HTML description into plain text: tags
// are removed (block tags become line breaks), entities decoded, and
// blank lines collapsed.
func StripHTML(s string) string {
	var b strings.Builder
	inTag := false
	var tag strings.Builder
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			tag.Reset()
		case r == '>' && inTag:
			inTag = false
			name := ""
			if f := strings.Fields(tag.String()); len(f) > 0 {
				name = strings.ToLower(strings.TrimLeft(f[0], "/"))
			}
			if name == "br" || name == "p" || name == "li" || name == "div" || strings.HasPrefix(name, "h") {
				b.WriteByte('\n')
			}
		case inTag:
			tag.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	text := html.UnescapeString(b.String())
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// atoiOr parses a header number, or returns def when it is absent or bad.
func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}
