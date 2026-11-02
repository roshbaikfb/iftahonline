# site

The platform's own public website on its domain (`iftahonline.com`;
`iftahonline.localhost:18081` in development), ADR-0071. It says what
افتح أونلاين is, what it costs and how to start, for merchants, search
engines and AI assistants.

## Public API

| Symbol | What it is |
|---|---|
| `New(Deps)` / `Module.Handler()` / `Module.Domain()` | The site and the domain it answers on |
| `Deps.Plans` (`site.Plans` = `billing.Showcase`) | The plans on sale, from `billing.NewShowcase` |
| `Deps.Font` (`site.Font`) | The brand typeface's faces and preloads, from `storefront.Shop.BrandFont` |
| `DefaultWhatsApp` | The support number until the console sets it (S4) |
| `DefaultCountryHeader` | `CF-IPCountry`: the CDN's visitor country |

It owns no tables, emits no events and has no HTTP API of its own.
`server.MountPlatformSite` mounts it in the storefront role.

## Pages and files

| Path | What | Cache |
|---|---|---|
| `/` | Home: hero, features, tools, steps, trust, store moving, plans in pounds, FAQ (5), CTA | `public, max-age=300, s-maxage=3600` |
| `/pricing` | Plans in the visitor's currency, monthly/annual, comparison from the features catalog | `private`, `Vary: CF-IPCountry` |
| `/faq` | Every answer (10) | public |
| `/features` | The six features, each linking its page | public |
| `/features/{slug}` | One feature: how it works, what the merchant controls, its questions, the other features. Slugs: `whatsapp-confirmation`, `fake-orders-protection`, `net-profit`, `shipping-cod`, `online-store`, `marketing` (copy in `features.go`) | public |
| `/about` | Why we built it, values, the company card (UK flag, address), contact | public |
| `/terms`, `/privacy` | Draft legal texts (`legal.go`); a lawyer reviews them before launch (go-live §13) | public |
| `/robots.txt` | Allow all; the AI crawlers named; the sitemap | public |
| `/sitemap.xml` | The pages, with `lastmod` (`updated` in `content.go`) | public |
| `/llms.txt`, `/llms-full.txt` | Plain facts for AI assistants; the full one adds the plans in pounds and every answer | public |
| anything else | 404 page, `noindex` | public |
| `/en`, `/en/…` | The same pages in English (ADR-0072); crawler files stay at the root | as the Arabic page |
| `www.{domain}/…` | 301 to `{domain}/…` | — |

## Rules

- **Languages (ADR-0072):**
  - Arabic is at the root and English is under `/en/`, with the same
    paths.
  - Each page links its twin with `hreflang`, and `x-default` is the
    Arabic page.
  - Words live in `i18n.go` (Arabic) and `content_en.go` (English);
    the templates hold none.
  - English plan and feature names come from billing's `NameEn`.

- **Currency:** pounds for Egypt and for an unknown country, dollars for
  any other. `?currency=EGP|USD` overrides it; the canonical URL stays
  `/pricing`.
- **Dollar prices:** a plan without a dollar price shows «قريبًا», and
  the page offers WhatsApp. Pounds are never shown in their place.
- **Plan cards:** the bullets come from each plan's grants (products,
  orders, messages, protection, profit, card payments, domain, staff).
  The first subscription plan is «موصى بها».
- **Copy:** merchant benefits only, never how the platform is built
  (`TestEveryPage` rejects technical words on every page).
  Example screens are labelled «مثال توضيحي».
- **Security:**
  - No script runs.
  - The stylesheet and the font faces are inlined under a hashed CSP.
  - Inline style attributes are not allowed; the chart bars use classes.
  - Other headers: `nosniff`, `Referrer-Policy`, `Permissions-Policy`,
    `COOP`.

## schema.org

| Page | Types |
|---|---|
| `/` | `Organization` (variants, support contact, parent Codezilo Ltd), `WebSite`, `SoftwareApplication` (offers), `FAQPage` |
| `/pricing` | `SoftwareApplication`, `BreadcrumbList` |
| `/faq` | `FAQPage`, `BreadcrumbList` |
| `/features` | `BreadcrumbList` |
| `/features/{slug}` | `BreadcrumbList` (home → features → it), `FAQPage` |
| `/about` | `Organization`, `AboutPage`, `BreadcrumbList` |
| `/terms`, `/privacy` | `BreadcrumbList` |

## Still to come

- **S4:** the console edits the FAQ, articles (guides), founders, team,
  the founding story, the company number and contact details. The about
  page shows founders and team once they are set.
- **The stores' own SEO:** robots, sitemap, Product JSON-LD.
- **`ads.txt` on the domain**, for AdSense (go-live §11).
- **The Open Graph image**, with the logo.
