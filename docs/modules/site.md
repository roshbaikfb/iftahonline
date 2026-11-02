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
| `/robots.txt` | Allow all; the AI crawlers named; the sitemap | public |
| `/sitemap.xml` | The pages, with `lastmod` (`updated` in `content.go`) | public |
| `/llms.txt`, `/llms-full.txt` | Plain facts for AI assistants; the full one adds the plans in pounds and every answer | public |
| anything else | 404 page, `noindex` | public |
| `www.{domain}/…` | 301 to `{domain}/…` | — |

## Rules

- **Currency:** pounds for Egypt and for an unknown country, dollars for
  any other. `?currency=EGP|USD` overrides it; the canonical URL stays
  `/pricing`.
- **Dollar prices:** a plan without a dollar price shows «قريبًا», and
  the page offers WhatsApp. Pounds are never shown in their place.
- **Plan cards:** the bullets come from each plan's grants (products,
  orders, messages, protection, profit, card payments, domain, staff).
  The first subscription plan is «موصى بها».
- **Copy:** merchant benefits only, never how the platform is built.
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

## Still to come

- **S3:** feature pages, the about page (founders and team), legal
  pages, the English site with `hreflang`.
- **S4:** the console edits the FAQ, articles (guides), founders, team
  and contact details.
- **The stores' own SEO:** robots, sitemap, Product JSON-LD.
- **`ads.txt` on the domain**, for AdSense (go-live §11).
- **The Open Graph image**, with the logo.
