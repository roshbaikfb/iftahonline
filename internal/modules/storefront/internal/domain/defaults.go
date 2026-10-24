package domain

import "encoding/json"

// DefaultHome is the home page of a store whose merchant has not saved
// one: a hero with the store's name whose button leads to the newest
// products, the categories, the usual trust badges, the newest products
// and the WhatsApp invitation (shown only once the store has a number).
// Empty texts are filled at render time in the shopper's language, so
// the defaults carry no language of their own.
func DefaultHome() Page {
	return Page{
		Kind: KindHome, Slug: HomeSlug, Status: StatusPublished,
		Sections: []Section{
			{ID: "hero", Type: SectionHero, Settings: json.RawMessage(`{"align":"start","button_link":"#s-newest"}`)},
			{ID: "cats", Type: SectionCategoryTiles, Settings: json.RawMessage(`{}`)},
			{ID: "trust", Type: SectionTrustBadges, Settings: json.RawMessage(`{"items":[{"icon":"cod"},{"icon":"shipping"},{"icon":"returns"}]}`)},
			{ID: "newest", Type: SectionProductGrid, Settings: json.RawMessage(`{"source":"newest","limit":8}`)},
			{ID: "help", Type: SectionWhatsAppCTA, Settings: json.RawMessage(`{}`)},
		},
	}
}

// DefaultPolicy is a policy the merchant has not written: an unpublished
// page with no body, which the storefront does not link to.
func DefaultPolicy(policy string) Page {
	return Page{Kind: KindPolicy, Slug: policy, Status: StatusDraft, Sections: []Section{}}
}
