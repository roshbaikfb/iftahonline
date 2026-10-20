package sections

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The settings of each section type, as the renderer uses them. Stored
// settings were validated against the schemas on save, so decoding only
// fills these in; fields keep the schema's names.

// Hero is a large opening banner.
type Hero struct {
	Heading        string   `json:"heading"`
	Subheading     string   `json:"subheading"`
	ButtonLabel    string   `json:"button_label"`
	ButtonLink     string   `json:"button_link"`
	ImageProductID *ulid.ID `json:"image_product_id"`
	Align          string   `json:"align"`
}

// ProductGrid shows products as cards.
type ProductGrid struct {
	Title      string    `json:"title"`
	Source     string    `json:"source"` // newest, category or manual
	CategoryID *ulid.ID  `json:"category_id"`
	ProductIDs []ulid.ID `json:"product_ids"`
	Limit      int       `json:"limit"`
}

// ProductLanding is one product, big.
type ProductLanding struct {
	ProductID       ulid.ID  `json:"product_id"`
	Headline        string   `json:"headline"`
	Highlights      []string `json:"highlights"`
	ShowDescription *bool    `json:"show_description"`
	CTALabel        string   `json:"cta_label"`
}

// Review is one testimonial.
type Review struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Rating   int    `json:"rating"`
	Text     string `json:"text"`
}

// Reviews shows testimonials.
type Reviews struct {
	Title string   `json:"title"`
	Items []Review `json:"items"`
}

// QA is one FAQ entry.
type QA struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// FAQ shows questions and answers.
type FAQ struct {
	Title string `json:"title"`
	Items []QA   `json:"items"`
}

// Badge is one trust badge.
type Badge struct {
	Icon  string `json:"icon"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// TrustBadges shows small promises with icons.
type TrustBadges struct {
	Items []Badge `json:"items"`
}

// Countdown shows the time left until an offer ends.
type Countdown struct {
	Title         string    `json:"title"`
	Subtitle      string    `json:"subtitle"`
	EndsAt        time.Time `json:"ends_at"`
	HideWhenEnded *bool     `json:"hide_when_ended"`
}

// Decode turns stored settings into the type's Go value (Hero,
// ProductGrid, ...), with schema defaults applied.
func Decode(t domain.SectionType, raw json.RawMessage) (any, error) {
	var v any
	switch t {
	case domain.SectionHero:
		v = &Hero{Align: "center"}
	case domain.SectionProductGrid:
		v = &ProductGrid{Limit: 8}
	case domain.SectionProductLanding:
		v = &ProductLanding{}
	case domain.SectionReviews:
		v = &Reviews{}
	case domain.SectionFAQ:
		v = &FAQ{}
	case domain.SectionTrustBadges:
		v = &TrustBadges{}
	case domain.SectionCountdown:
		v = &Countdown{}
	default:
		return nil, fmt.Errorf("sections: unknown type %q", t)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return nil, fmt.Errorf("sections: decode %s: %w", t, err)
	}
	return v, nil
}
