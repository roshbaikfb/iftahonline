package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// sitePage is an indexable page, for sitemap.xml and llms.txt; path is
// the page's path in its language (without /en).
type sitePage struct{ path, name, about string }

// sitePages are the site's indexable pages in language l: the fixed
// ones, with each feature's page after the features index.
func sitePages(l *language) []sitePage {
	t, about := l.t, [...]string{
		"ما هي افتح أونلاين ولمن، ومميزاتها، وخطوات البدء.", "الباقات وأسعارها بالجنيه في مصر وبالدولار في باقي الدول.",
		"كل مميزات افتح أونلاين، ولكل ميزة صفحتها.", "إجابات عن البدء والتأكيد والحماية والأسعار ونقل المتاجر.",
		"من نحن، وما نؤمن به، والشركة التي تدير المنصة.", "شروط استخدام المنصة والباقات والإلغاء.", "كيف نجمع البيانات ونستخدمها ونحميها.",
	}
	if l == english {
		about = [...]string{
			"What iftahonline is and who it's for, its features, and how to start.", "The plans and their prices, in pounds in Egypt and dollars elsewhere.",
			"Every iftahonline feature, each with its own page.", "Answers about starting, confirmation, protection, prices and moving stores.",
			"Who we are, what we believe, and the company that runs the platform.", "The terms for using the platform, its plans and cancellation.",
			"How we collect, use and protect data.",
		}
	}
	out := []sitePage{{"/", t.Home, about[0]}, {"/pricing", t.Pricing, about[1]}, {"/features", t.Features, about[2]}}
	for _, f := range l.c.featurePages {
		out = append(out, sitePage{"/features/" + f.slug, f.name, f.short})
	}
	return append(out, sitePage{"/faq", t.FAQ, about[3]}, sitePage{"/about", t.About, about[4]},
		sitePage{"/terms", l.c.terms.name, about[5]}, sitePage{"/privacy", l.c.privacy.name, about[6]})
}

// aiCrawlers are the AI assistants' crawlers, allowed by name so the
// site is read and cited (the owner's decision).
var aiCrawlers = []string{ //nolint:gochecknoglobals // constant list
	"GPTBot", "OAI-SearchBot", "ChatGPT-User", "Google-Extended", "PerplexityBot", "ClaudeBot", "Claude-SearchBot", "Applebot-Extended", "CCBot",
}

func (s *Site) robots() string {
	var b strings.Builder
	b.WriteString("User-agent: *\nAllow: /\n\n")
	for _, bot := range aiCrawlers {
		fmt.Fprintf(&b, "User-agent: %s\nAllow: /\n\n", bot)
	}
	fmt.Fprintf(&b, "Sitemap: %s/sitemap.xml\n", s.origin())
	return b.String()
}

func (s *Site) sitemap(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	// Each page in both languages, each naming the other (Google's
	// hreflang in sitemaps).
	englishGuides := s.guideSitePages(ctx, english)
	for _, l := range []*language{arabic, english} {
		for _, p := range sitePages(l) {
			s.sitemapURL(&b, l, p.path, true)
		}
		// Guides: an Arabic-only guide has no English alternate.
		for _, p := range s.guideSitePages(ctx, l) {
			s.sitemapURL(&b, l, p.path, l == english || hasEnglishPath(englishGuides, p.path))
		}
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// sitemapURL is one page of l in the sitemap, with its alternates (the
// English one when withEnglish).
func (s *Site) sitemapURL(b *strings.Builder, l *language, path string, withEnglish bool) {
	fmt.Fprintf(b, "<url><loc>%s%s</loc><lastmod>%s</lastmod>", s.origin(), l.at(path), updated)
	alts := []struct {
		code string
		l    *language
	}{{"ar", arabic}, {"en", english}, {"x-default", arabic}}
	for _, alt := range alts {
		if alt.l == english && !withEnglish {
			continue
		}
		fmt.Fprintf(b, `<xhtml:link rel="alternate" hreflang="%s" href="%s%s"/>`, alt.code, s.origin(), alt.l.at(path))
	}
	b.WriteString("</url>\n")
}

// hasEnglishPath says whether pages holds path.
func hasEnglishPath(pages []sitePage, path string) bool {
	for _, p := range pages {
		if p.path == path {
			return true
		}
	}
	return false
}

// llms is /llms.txt (llmstxt.org): a short, factual account of the
// platform for AI assistants, with the pages to read; full adds every
// answer and the plans in pounds.
func (s *Site) llms(ctx context.Context, full bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n\n> %s %s منصة لإنشاء المتاجر الإلكترونية لتجار مصر والخليج الذين يبيعون بالدفع عند الاستلام.\n\n", brandAR, brandLatin, brandAR, "هي")
	fmt.Fprintf(&b, "يُكتب الاسم أيضًا: %s.\n\n", brandVariants)
	b.WriteString("## ما تقدمه\n\n")
	b.WriteString("- متجر إلكتروني بالعربية والإنجليزية بثلاثة أشكال جاهزة، وصفحات هبوط بطلب من خطوة واحدة، ودومين خاص.\n")
	b.WriteString("- تأكيد الطلبات على واتساب بزرّي تأكيد وإلغاء، ورسالة نصية بديلة، وإلغاء تلقائي لما لم يُرد عليه.\n")
	b.WriteString("- حماية من الطلبات الوهمية: تقييم لكل طلب من نتائج التوصيل في متاجر المنصة كلها، وقواعد يختارها التاجر.\n")
	b.WriteString("- لوحة الربح الصافي بعد تكلفة المنتج والشحن والتحصيل والمرتجعات والإعلانات.\n")
	b.WriteString("- الشحن والتحصيل، والدفع بالبطاقة عبر Paymob، وبكسلات الإعلانات، والكوبونات، وكتالوج المنتجات.\n\n")
	b.WriteString("## الصفحات\n\n")
	for _, p := range append(sitePages(arabic), s.guideSitePages(ctx, arabic)...) {
		fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.name, s.origin(), p.path, p.about)
	}
	b.WriteString("\n## In English\n\n")
	fmt.Fprintf(&b, "%s (also eftahonline, in Arabic %s) is an online store platform for merchants in Egypt and the Gulf who sell with cash on delivery: "+
		"order confirmation on WhatsApp, fake-order protection, a net profit dashboard, shipping and collection, and a store in Arabic and English.\n\n", brandEN, brandAR)
	for _, p := range append(sitePages(english), s.guideSitePages(ctx, english)...) {
		fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.name, s.origin(), english.at(p.path), p.about)
	}
	if !full {
		fmt.Fprintf(&b, "\nالنسخة الكاملة: %s/llms-full.txt\n", s.origin())
		return b.String()
	}
	if pr, err := s.pricing(ctx, currencyEGP, arabic); err == nil {
		b.WriteString("\n## الباقات في مصر\n\n")
		for _, p := range pr.Plans {
			switch {
			case p.Free:
				fmt.Fprintf(&b, "- %s: مجانية دائمًا", p.Name)
			case p.Pays && p.Fee != "":
				fmt.Fprintf(&b, "- %s: بلا اشتراك، %s من كل طلب يُسلَّم", p.Name, p.Fee)
			case p.Monthly != "":
				fmt.Fprintf(&b, "- %s: %s في الشهر", p.Name, p.Monthly)
			default:
				continue
			}
			if len(p.Lines) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(p.Lines, "، "))
			}
			b.WriteString(".\n")
		}
		b.WriteString("\nخارج مصر تُسعَّر الباقات بالدولار الأمريكي.\n")
	}
	// The FAQ is the console's: left out while it cannot be read.
	if ct, err := s.d.Content.Content(ctx); err == nil && len(ct.FAQs) > 0 {
		b.WriteString("\n## أسئلة شائعة\n\n")
		for _, f := range faqsIn(arabic, ct.FAQs) {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", f.q, f.a)
		}
	}
	return b.String()
}

// The schema.org data, as JSON-LD (https://schema.org).

// organizationLD is the platform's organization, named in the page's
// language, with the console's contact details and profiles. Both
// languages describe one organization (one @id).
func (s *Site) organizationLD(p *pageCtx) any {
	l, set := p.l, p.settings
	org := map[string]any{
		"@context": "https://schema.org", "@type": "Organization", "@id": s.origin() + "/#organization",
		"name": l.c.brand, "alternateName": alternateNames(l), "url": s.origin() + l.at("/"),
		"description": l.c.tagline,
		"parentOrganization": map[string]any{
			"@type": "Organization", "name": company.name,
			"address": map[string]any{"@type": "PostalAddress", "streetAddress": company.street, "addressLocality": company.city,
				"postalCode": company.postcode, "addressCountry": company.country},
		},
	}
	var points []any
	for _, n := range []string{set.WhatsApp, set.WhatsAppIntl} {
		if n != "" {
			points = append(points, map[string]any{"@type": "ContactPoint", "telephone": n, "contactType": "customer support",
				"availableLanguage": []string{"ar", "en"}})
		}
	}
	if set.Email != "" {
		org["email"] = set.Email
		points = append(points, map[string]any{"@type": "ContactPoint", "email": set.Email, "contactType": "customer support",
			"availableLanguage": []string{"ar", "en"}})
	}
	if len(points) > 0 {
		org["contactPoint"] = points
	}
	var profiles []string
	for _, sl := range p.socials {
		profiles = append(profiles, sl.url)
	}
	if len(profiles) > 0 {
		org["sameAs"] = profiles
	}
	if p.logo != "" {
		org["logo"] = p.logo
	}
	var founders []any
	for _, f := range p.founders {
		person := map[string]any{"@type": "Person", "name": f.name}
		if f.role != "" {
			person["jobTitle"] = f.role
		}
		if f.photo != "" {
			person["image"] = f.photo
		}
		var links []string
		for _, u := range []string{f.linkedIn, f.x} {
			if u != "" {
				links = append(links, u)
			}
		}
		if len(links) > 0 {
			person["sameAs"] = links
		}
		founders = append(founders, person)
	}
	if len(founders) > 0 {
		org["founder"] = founders
	}
	if set.CompanyNumber != "" {
		parent, _ := org["parentOrganization"].(map[string]any) //nolint:errcheck // built just above
		parent["identifier"] = map[string]any{"@type": "PropertyValue", "propertyID": "Companies House", "value": set.CompanyNumber}
		parent["url"] = set.CompanyHouseURL()
	}
	return org
}

func (s *Site) websiteLD(l *language) any {
	return map[string]any{
		"@context": "https://schema.org", "@type": "WebSite", "@id": s.origin() + "/#website", "url": s.origin() + "/",
		"name": l.c.brand, "alternateName": alternateNames(l), "inLanguage": []string{"ar", "en"},
		"publisher": map[string]any{"@id": s.origin() + "/#organization"},
	}
}

// alternateNames are the brand's other spellings, besides its name in l.
func alternateNames(l *language) []string {
	var out []string
	for _, n := range append([]string{brandAR}, strings.Split(brandVariants, "، ")...) {
		if n != l.c.brand {
			out = append(out, n)
		}
	}
	return out
}

// applicationLD is the platform as software, with its plans as offers in
// pr's currency (the ones priced in it).
func (s *Site) applicationLD(l *language, pr pricing) any {
	var offers []any
	for _, p := range pr.Plans {
		switch {
		case p.Free:
			offers = append(offers, map[string]any{"@type": "Offer", "name": p.Name, "price": "0", "priceCurrency": pr.Currency})
		case p.Monthly != "":
			offers = append(offers, map[string]any{"@type": "Offer", "name": p.Name, "price": p.MonthlyAt, "priceCurrency": pr.Currency,
				"priceSpecification": map[string]any{"@type": "UnitPriceSpecification", "price": p.MonthlyAt, "priceCurrency": pr.Currency,
					"unitCode": "MON"}})
		}
	}
	app := map[string]any{
		"@context": "https://schema.org", "@type": "SoftwareApplication", "name": l.c.brand, "alternateName": alternateNames(l),
		"applicationCategory": "BusinessApplication", "applicationSubCategory": "E-commerce platform", "operatingSystem": "Web",
		"url": s.origin() + l.at("/"), "description": l.c.tagline, "inLanguage": l.code,
		"publisher": map[string]any{"@id": s.origin() + "/#organization"},
	}
	if len(offers) > 0 {
		app["offers"] = offers
	}
	return app
}

func (s *Site) faqLD(list []faq) any {
	items := make([]any, len(list))
	for i, f := range list {
		items[i] = map[string]any{"@type": "Question", "name": f.q, "acceptedAnswer": map[string]any{"@type": "Answer", "text": f.a}}
	}
	return map[string]any{"@context": "https://schema.org", "@type": "FAQPage", "mainEntity": items}
}

// crumb is a step of a page's trail, after the home page.
type crumb struct{ name, path string }

func (s *Site) breadcrumbLD(l *language, trail ...crumb) any {
	items := []any{map[string]any{"@type": "ListItem", "position": 1, "name": l.t.Home, "item": s.origin() + l.at("/")}}
	for i, c := range trail {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 2, "name": c.name, "item": s.origin() + l.at(c.path)})
	}
	return map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items}
}

// aboutLD is the about page, about the organization.
func (s *Site) aboutLD(p *pageCtx) any {
	return map[string]any{
		"@context": "https://schema.org", "@type": "AboutPage", "url": p.url(p.l), "name": p.title, "inLanguage": p.l.code,
		"mainEntity": map[string]any{"@id": s.origin() + "/#organization"},
	}
}

// ldScript is one JSON-LD block, safe inside <script>: encoding/json
// escapes <, > and & so nothing can close the element.
func ldScript(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(b) + `</script>`
}
