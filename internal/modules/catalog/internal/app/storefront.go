package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/search"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/slug"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Storefront limits.
const (
	DefaultStorefrontLimit = 24
	MaxStorefrontLimit     = 48
	MaxStorefrontIDs       = 50 // products one page section may pick
)

// ImageSigner builds a signed delivery URL (media.Signer).
type ImageSigner interface {
	URL(objectKey string, size int, format string) string
}

// Storefront is the read side shoppers see: active, live products only,
// with signed image URLs. It needs only the database and the signer, so
// the storefront role builds it without object storage or search.
type Storefront struct {
	db     postgres.Beginner
	signer ImageSigner
	index  SearchReader // nil: the storefront offers no search
}

// SearchReader is the index's read side (search.Client with a
// search-only key): the store filter is added by the client from ctx.
type SearchReader interface {
	Search(ctx context.Context, q search.Query) (search.Result, error)
}

// WithSearch lets shoppers search through index.
func (s *Storefront) WithSearch(index SearchReader) *Storefront {
	s.index = index
	return s
}

// Shoppers' search limits: one page of a grid, a few pages deep.
const (
	ShopSearchPageSize = 24
	MaxShopSearchPage  = 20
	MaxShopSearchLen   = 100
)

// ErrSearchUnavailable means the storefront has no search configured.
var ErrSearchUnavailable = errors.New("catalog: search is not available")

// StorefrontSearch is a shopper's search: some text and a page from 1.
type StorefrontSearch struct {
	Text string
	Page int
}

// StorefrontSearchResult is one page of matching active products.
type StorefrontSearchResult struct {
	Items []StorefrontProduct
	Total int64 // the index's estimate
	More  bool  // another page follows
}

// Search finds the store's active products matching q's text (normalized
// like the documents: Arabic forms, diacritics, digits), in the index's
// order. The index gives IDs; the products come from the database in one
// read, so a stale document never shows a product that is gone or no
// longer active.
func (s *Storefront) Search(ctx context.Context, q StorefrontSearch) (StorefrontSearchResult, error) {
	if s.index == nil {
		return StorefrontSearchResult{}, ErrSearchUnavailable
	}
	text := arabic.NormalizeSearch(q.Text)
	var fields []errs.FieldError
	if text == "" || utf8.RuneCountInString(q.Text) > MaxShopSearchLen || !utf8.ValidString(q.Text) {
		fields = append(fields, errs.FieldError{Field: "q", Code: "length", Message: fmt.Sprintf("q must be 1 to %d characters", MaxShopSearchLen)})
	}
	if q.Page < 1 || q.Page > MaxShopSearchPage {
		fields = append(fields, errs.FieldError{Field: "page", Code: "range", Message: fmt.Sprintf("page must be between 1 and %d", MaxShopSearchPage)})
	}
	if len(fields) > 0 {
		return StorefrontSearchResult{}, errs.Validation(fields...)
	}
	res, err := s.index.Search(ctx, search.Query{Text: text, Status: string(domain.StatusActive), Limit: ShopSearchPageSize,
		Offset: (q.Page - 1) * ShopSearchPageSize})
	if err != nil {
		if errors.Is(err, tenant.ErrNoTenant) {
			return StorefrontSearchResult{}, err
		}
		return StorefrontSearchResult{}, errs.Unavailable(time.Second, err)
	}
	ids := make([]ulid.ID, 0, len(res.Hits))
	for _, h := range res.Hits {
		if id, perr := ulid.Parse(h.ID); perr == nil {
			ids = append(ids, id)
		}
	}
	items, err := s.ProductsByIDs(ctx, ids)
	if err != nil {
		return StorefrontSearchResult{}, err
	}
	return StorefrontSearchResult{Items: items, Total: res.EstimatedTotal,
		More: int64(q.Page*ShopSearchPageSize) < res.EstimatedTotal && q.Page < MaxShopSearchPage}, nil
}

// NewStorefront builds the storefront reader.
func NewStorefront(db postgres.Beginner, signer ImageSigner) *Storefront {
	return &Storefront{db: db, signer: signer}
}

// StorefrontProduct is a product as a shopper sees it.
type StorefrontProduct struct {
	ID          ulid.ID
	Slug        string
	Title       string
	Description string
	Options     []domain.Option
	Variants    []StorefrontVariant // at least one, by position
	CategoryIDs []ulid.ID
	Images      []StorefrontImage // ready images only, by position
	// Sample is a product from the setup wizard's template (ADR-0034):
	// shown, marked, and never sellable (no variant is available).
	Sample bool
	// SizeChart is the product's size chart, on ProductBySlug only (the
	// product page); nil for none and in lists.
	SizeChart *StorefrontSizeChart
	UpdatedAt time.Time
}

// StorefrontSizeChart is a size chart as the product page shows it, in
// centimetres: column names (the size first) and rows as long.
type StorefrontSizeChart struct {
	Header []string
	Body   [][]string
}

// StorefrontVariant is a variant as a shopper sees it. Stock levels stay
// private: only whether one unit can be bought, and how many are left
// when they are few (domain.MaxShownLeft at most).
type StorefrontVariant struct {
	ID           ulid.ID
	OptionValues []string
	Price        money.Money
	CompareAt    *money.Money
	Available    bool
	Left         int // units left when 1 to domain.MaxShownLeft; 0 otherwise
}

// StorefrontImage is a ready image with its renditions.
type StorefrontImage struct {
	Alt        string
	Width      int // of the master
	Height     int
	Renditions []Rendition // every format × size, smallest first per format
	// FeedURL is the image for product feeds (a JPEG up to
	// media.FeedSize), on StorefrontQuery.Feed pages only.
	FeedURL string
}

// Rendition is one delivery URL with the pixel size it is served at.
type Rendition struct {
	Format string // avif or webp
	Width  int
	Height int
	URL    string
}

// Available reports whether any variant can be bought.
func (p StorefrontProduct) Available() bool {
	for _, v := range p.Variants {
		if v.Available {
			return true
		}
	}
	return false
}

// PriceFrom returns the variant with the lowest price, preferring
// available variants.
func (p StorefrontProduct) PriceFrom() StorefrontVariant {
	var best StorefrontVariant
	found := false
	for _, v := range p.Variants {
		better := !found || (v.Available && !best.Available) ||
			(v.Available == best.Available && v.Price.Amount() < best.Price.Amount())
		if better {
			best, found = v, true
		}
	}
	return best
}

// StorefrontQuery selects a page of a store's products, newest first.
type StorefrontQuery struct {
	CategoryID *ulid.ID
	Cursor     string
	Limit      int
	// Feed fills each image's FeedURL (a product feed's page).
	Feed bool
}

// StorefrontPage is one page of products.
type StorefrontPage struct {
	Items      []StorefrontProduct
	NextCursor string // empty on the last page
}

// ProductBySlug returns the active product at productSlug, or
// ErrProductNotFound. A malformed slug costs no query.
func (s *Storefront) ProductBySlug(ctx context.Context, productSlug string) (StorefrontProduct, error) {
	if _, ok := slug.Check("slug", productSlug); !ok {
		return StorefrontProduct{}, ErrProductNotFound
	}
	var p domain.Product
	var chart *StorefrontSizeChart
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if p, txErr = store.GetActiveProductBySlug(ctx, tx, productSlug); txErr != nil || p.SizeChartID == nil {
			return txErr
		}
		c, txErr := store.SizeChartOnly(ctx, tx, *p.SizeChartID)
		if txErr == nil {
			chart = &StorefrontSizeChart{Header: c.Header, Body: c.Body}
		}
		return txErr
	})
	if err != nil {
		return StorefrontProduct{}, notFound(err, ErrProductNotFound)
	}
	v := s.view(p, false)
	if len(v.Variants) == 0 {
		return StorefrontProduct{}, ErrProductNotFound // every variant hidden: nothing to sell or price
	}
	v.SizeChart = chart
	return v, nil
}

// ProductsByIDs returns the active products among ids in the order
// given, skipping the rest and repeats. At most MaxStorefrontIDs are
// read.
func (s *Storefront) ProductsByIDs(ctx context.Context, ids []ulid.ID) ([]StorefrontProduct, error) {
	uniq := make([]ulid.ID, 0, min(len(ids), MaxStorefrontIDs))
	seen := make(map[ulid.ID]bool, len(ids))
	for _, id := range ids {
		if len(uniq) == MaxStorefrontIDs {
			break
		}
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return []StorefrontProduct{}, nil
	}
	var ps []domain.Product
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		ps, txErr = store.ListActiveProductsByIDs(ctx, tx, uniq)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	return s.views(ps, false), nil
}

// ListProducts returns a page of active products in a constant number of
// queries.
func (s *Storefront) ListProducts(ctx context.Context, q StorefrontQuery) (StorefrontPage, error) {
	active := domain.StatusActive
	f := store.ProductFilter{Status: &active, CategoryID: q.CategoryID, Limit: q.Limit}
	if f.Limit <= 0 {
		f.Limit = DefaultStorefrontLimit
	}
	f.Limit = min(f.Limit, MaxStorefrontLimit)
	page, err := listPage(ctx, s.db, f, q.Cursor)
	if err != nil {
		return StorefrontPage{}, err
	}
	return StorefrontPage{Items: s.views(page.Items, q.Feed), NextCursor: page.NextCursor}, nil
}

// SellableVariants is Service.SellableVariants for the storefront role
// (a cart's lines).
func (s *Storefront) SellableVariants(ctx context.Context, ids []ulid.ID) ([]domain.SellableVariant, error) {
	return sellableVariants(ctx, s.db, ids)
}

// Categories returns the store's categories, by position.
func (s *Storefront) Categories(ctx context.Context) ([]domain.Category, error) {
	var out []domain.Category
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListCategories(ctx, tx)
		return txErr
	})
	return out, err
}

// views are ps as shoppers see them; feed adds the feed images.
func (s *Storefront) views(ps []domain.Product, feed bool) []StorefrontProduct {
	out := make([]StorefrontProduct, 0, len(ps))
	for _, p := range ps {
		if v := s.view(p, feed); len(v.Variants) > 0 { // a product without shown variants cannot be sold or priced
			out = append(out, v)
		}
	}
	return out
}

func (s *Storefront) view(p domain.Product, feed bool) StorefrontProduct {
	v := StorefrontProduct{
		ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, Options: p.Options,
		CategoryIDs: p.CategoryIDs, Sample: p.Sample, UpdatedAt: p.UpdatedAt,
		Variants: make([]StorefrontVariant, 0, len(p.Variants)),
		Images:   make([]StorefrontImage, 0, len(p.Images)),
	}
	for _, pv := range p.Variants {
		if pv.Hidden {
			continue // off sale: shoppers never see it (ADR-0035)
		}
		sv := StorefrontVariant{
			ID: pv.ID, OptionValues: pv.OptionValues, Price: pv.Price, CompareAt: pv.CompareAt, Available: pv.Stock.CanSell(1) && !p.Sample,
		}
		if !p.Sample {
			sv.Left = pv.Stock.Left()
		}
		v.Variants = append(v.Variants, sv)
	}
	for _, img := range p.Images {
		if img.Status == domain.ImageReady {
			v.Images = append(v.Images, s.image(img, feed))
		}
	}
	return v
}

func (s *Storefront) image(img domain.Image, feed bool) StorefrontImage {
	out := StorefrontImage{Alt: img.Alt, Width: img.Width, Height: img.Height}
	if feed {
		out.FeedURL = s.signer.URL(img.ObjectKey, media.FeedSize, media.FeedFormat)
	}
	for _, f := range media.Formats() {
		last := 0
		for _, p := range media.Presets() {
			w, h := Fit(img.Width, img.Height, p.Size)
			if w == last {
				break // a small master: larger presets would serve the same pixels
			}
			last = w
			out.Renditions = append(out.Renditions, Rendition{Format: f, Width: w, Height: h, URL: s.signer.URL(img.ObjectKey, p.Size, f)})
		}
	}
	return out
}

// Fit returns the size of a w × h image fitted into a box × box square,
// never enlarged (imgproxy's rs:fit with enlarge off).
func Fit(w, h, box int) (int, int) {
	if w <= 0 || h <= 0 || (w <= box && h <= box) {
		return w, h
	}
	scale := float64(box) / float64(max(w, h))
	return max(int(math.Round(float64(w)*scale)), 1), max(int(math.Round(float64(h)*scale)), 1)
}
