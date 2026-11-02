package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// pages are the site's indexable pages, for sitemap.xml and llms.txt.
var pages = []struct{ path, name, about string }{ //nolint:gochecknoglobals // constant copy
	{"/", "الرئيسية", "ما هي افتح أونلاين ولمن، ومميزاتها، وخطوات البدء."},
	{"/pricing", "الأسعار", "الباقات وأسعارها بالجنيه في مصر وبالدولار في باقي الدول."},
	{"/faq", "الأسئلة الشائعة", "إجابات عن البدء والتأكيد والحماية والأسعار ونقل المتاجر."},
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

func (s *Site) sitemap() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range pages {
		fmt.Fprintf(&b, "<url><loc>%s%s</loc><lastmod>%s</lastmod></url>\n", s.origin(), p.path, updated)
	}
	b.WriteString("</urlset>\n")
	return b.String()
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
	for _, p := range pages {
		fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.name, s.origin(), p.path, p.about)
	}
	if !full {
		fmt.Fprintf(&b, "\nالنسخة الكاملة: %s/llms-full.txt\n", s.origin())
		return b.String()
	}
	if pr, err := s.pricing(ctx, currencyEGP); err == nil {
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
	b.WriteString("\n## أسئلة شائعة\n\n")
	for _, f := range faqs {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", f.q, f.a)
	}
	return b.String()
}

// The schema.org data, as JSON-LD (https://schema.org).

func (s *Site) organizationLD() any {
	org := map[string]any{
		"@context": "https://schema.org", "@type": "Organization", "@id": s.origin() + "/#organization",
		"name": brandAR, "alternateName": strings.Split(brandVariants, "، "), "url": s.origin() + "/",
		"description": tagline,
		"parentOrganization": map[string]any{
			"@type": "Organization", "name": company.name,
			"address": map[string]any{"@type": "PostalAddress", "streetAddress": company.street, "addressLocality": company.city,
				"postalCode": company.postcode, "addressCountry": company.country},
		},
	}
	if s.d.WhatsApp != "" {
		org["contactPoint"] = map[string]any{"@type": "ContactPoint", "telephone": s.d.WhatsApp, "contactType": "customer support",
			"availableLanguage": []string{"ar", "en"}}
	}
	return org
}

func (s *Site) websiteLD() any {
	return map[string]any{
		"@context": "https://schema.org", "@type": "WebSite", "@id": s.origin() + "/#website", "url": s.origin() + "/",
		"name": brandAR, "alternateName": brandLatin, "inLanguage": "ar", "publisher": map[string]any{"@id": s.origin() + "/#organization"},
	}
}

// applicationLD is the platform as software, with its plans as offers in
// pr's currency (the ones priced in it).
func (s *Site) applicationLD(pr pricing) any {
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
		"@context": "https://schema.org", "@type": "SoftwareApplication", "name": brandAR, "alternateName": brandLatin,
		"applicationCategory": "BusinessApplication", "applicationSubCategory": "E-commerce platform", "operatingSystem": "Web",
		"url": s.origin() + "/", "description": tagline, "inLanguage": "ar",
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

func (s *Site) breadcrumbLD(p *pageCtx, name string) any {
	return map[string]any{
		"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": []any{
			map[string]any{"@type": "ListItem", "position": 1, "name": "الرئيسية", "item": s.origin() + "/"},
			map[string]any{"@type": "ListItem", "position": 2, "name": name, "item": s.origin() + p.path},
		},
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
