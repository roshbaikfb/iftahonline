package web

import (
	"strconv"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Structured data (schema.org) for search engines: a product with its
// offers, and the breadcrumb that leads to it.

type ldOffer struct {
	Type          string `json:"@type"`
	Price         string `json:"price,omitempty"`
	LowPrice      string `json:"lowPrice,omitempty"`
	HighPrice     string `json:"highPrice,omitempty"`
	OfferCount    int    `json:"offerCount,omitempty"`
	PriceCurrency string `json:"priceCurrency"`
	Availability  string `json:"availability"`
	URL           string `json:"url"`
}

type ldProduct struct {
	Context     string   `json:"@context"`
	Type        string   `json:"@type"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Image       []string `json:"image,omitempty"`
	URL         string   `json:"url"`
	Offers      ldOffer  `json:"offers"`
}

type ldCrumb struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Item     string `json:"item"`
}

type ldBreadcrumbs struct {
	Context string    `json:"@context"`
	Type    string    `json:"@type"`
	Items   []ldCrumb `json:"itemListElement"`
}

// decimal writes m as a plain decimal ("1250.50") for structured data.
func decimal(m money.Money) string {
	units := m.Currency().MinorUnits()
	s := strconv.FormatInt(m.Amount(), 10)
	if units == 0 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for len(s) <= units {
		s = "0" + s
	}
	s = s[:len(s)-units] + "." + s[len(s)-units:]
	if neg {
		s = "-" + s
	}
	return s
}

// productLD is the structured data of a product page.
func productLD(p *pageCtx, pr catalog.StorefrontProduct, crumb *catalog.Category) []byte {
	url := p.abs(p.l, productPath(pr.Slug))
	avail := "https://schema.org/OutOfStock"
	if pr.Available() {
		avail = "https://schema.org/InStock"
	}
	from := pr.PriceFrom()
	offer := ldOffer{Type: "Offer", Price: decimal(from.Price), PriceCurrency: string(from.Price.Currency()), Availability: avail, URL: url}
	if len(pr.Variants) > 1 {
		lo, hi := pr.Variants[0].Price, pr.Variants[0].Price
		for _, v := range pr.Variants[1:] {
			if v.Price.Amount() < lo.Amount() {
				lo = v.Price
			}
			if v.Price.Amount() > hi.Amount() {
				hi = v.Price
			}
		}
		offer = ldOffer{Type: "AggregateOffer", LowPrice: decimal(lo), HighPrice: decimal(hi), OfferCount: len(pr.Variants),
			PriceCurrency: string(lo.Currency()), Availability: avail, URL: url}
	}
	prod := ldProduct{Context: "https://schema.org", Type: "Product", Name: pr.Title, URL: url, Offers: offer}
	if d := paragraphs(pr.Description); len(d) > 0 {
		prod.Description = d[0]
	}
	for _, img := range pr.Images {
		if pic := pictureOf(img, ""); pic.src != "" {
			prod.Image = append(prod.Image, pic.src)
		}
	}
	crumbs := ldBreadcrumbs{Context: "https://schema.org", Type: "BreadcrumbList", Items: []ldCrumb{
		{Type: "ListItem", Position: 1, Name: p.l.T(MsgHome), Item: p.abs(p.l, "/")},
	}}
	if crumb != nil {
		crumbs.Items = append(crumbs.Items, ldCrumb{Type: "ListItem", Position: 2, Name: crumb.Name, Item: p.abs(p.l, categoryPath(crumb.Slug))})
	}
	crumbs.Items = append(crumbs.Items, ldCrumb{Type: "ListItem", Position: len(crumbs.Items) + 1, Name: pr.Title, Item: url})
	return marshalLD([]any{prod, crumbs})
}
