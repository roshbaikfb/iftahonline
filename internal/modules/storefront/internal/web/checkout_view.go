package web

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// thumbSize is the picture size of a line's thumbnail (imgproxy's
// "thumb" preset), shown at 3.5rem.
const thumbSize = 160

// thumb is a line's picture in both formats.
type thumb struct{ avif, webp string }

// thumbOf signs the thumbnail of a product's picture; nil without one.
func (s *Shop) thumbOf(key string) *thumb {
	if key == "" {
		return nil
	}
	return &thumb{avif: s.signer.URL(key, thumbSize, "avif"), webp: s.signer.URL(key, thumbSize, "webp")}
}

// cartLine is a cart line with its picture.
type cartLine struct {
	app.CartLineView
	thumb *thumb
}

func (s *Shop) cartLines(v app.CartView) []cartLine {
	out := make([]cartLine, len(v.Lines))
	for i, l := range v.Lines {
		out[i] = cartLine{CartLineView: l, thumb: s.thumbOf(l.ImageKey)}
	}
	return out
}

// cartSummary is checkout's summary of the cart, with pictures.
func (s *Shop) cartSummary(v app.CartView) *orderSummary {
	sum := &orderSummary{subtotal: v.Subtotal}
	for _, l := range v.Lines {
		sum.lines = append(sum.lines, summaryLine{title: l.ProductTitle, options: joinOptions(l.OptionValues), qty: l.Qty, total: l.Total,
			thumb: s.thumbOf(l.ImageKey)})
		sum.units += l.Qty
	}
	return sum
}

// thanksView is the thank-you page: the order and what the page adds to
// it (pictures, where it goes, the WhatsApp question).
type thanksView struct {
	o        OrderSummary
	payURL   string
	thumbs   map[ulid.ID]*thumb
	place    string // "مدينة نصر، القاهرة"
	whatsApp templ.SafeURL
	// confirmWA is the "confirm on WhatsApp" link (ADR-0041), for a
	// cash-on-delivery order still waiting for its customer's answer.
	confirmWA templ.SafeURL
}

func (s *Shop) thanksView(ctx context.Context, p *pageCtx, o OrderSummary, payURL string) thanksView {
	v := thanksView{o: o, payURL: payURL, thumbs: map[ulid.ID]*thumb{}}
	ids := make([]ulid.ID, 0, len(o.Lines))
	for _, l := range o.Lines {
		ids = append(ids, l.VariantID)
	}
	// Pictures of what is still for sale, in one read; a failure only
	// leaves the lines without pictures.
	if vs, err := s.catalog.SellableVariants(ctx, ids); err == nil {
		for _, sv := range vs {
			v.thumbs[sv.VariantID] = s.thumbOf(sv.ImageKey)
		}
	}
	v.place = o.City
	if a, ok := geo.Lookup(p.v.country, o.Governorate); ok {
		if v.place != "" {
			v.place += p.l.T(MsgListSep)
		}
		v.place += a.Name(string(p.l))
	}
	if s.confirms != nil && !o.Card && o.DepositStatus == "" && !o.Cancelled {
		// A failure only leaves the page without the link: the paid
		// message goes out anyway.
		if link, err := s.confirms.WhatsAppLink(ctx, o.Ref, string(p.l)); err == nil && link != "" {
			v.confirmWA = templ.SafeURL(link) //nolint:gosec // our own wa.me link, the text escaped
		} else if err != nil {
			s.log.WarnContext(ctx, "storefront: confirm link unavailable", "err", err)
		}
	}
	if wa := p.v.theme.Contact.WhatsApp; wa != "" {
		text := fmt.Sprintf(p.l.T(MsgOrderQuestionText), o.Code)
		v.whatsApp = templ.SafeURL(string(waLink(wa)) + "?text=" + url.QueryEscape(text)) //nolint:gosec // our own wa.me link, the text escaped
	}
	return v
}

// Pieces counts items the way each language says it: قطعة واحدة،
// قطعتان، 3 قطع، 11 قطعة.
func (l Locale) Pieces(n int) string {
	if l == English {
		if n == 1 {
			return "1 item"
		}
		return strconv.Itoa(n) + " items"
	}
	switch {
	case n == 1:
		return "قطعة واحدة"
	case n == 2:
		return "قطعتان"
	case n <= 10:
		return strconv.Itoa(n) + " قطع"
	}
	return strconv.Itoa(n) + " قطعة"
}
