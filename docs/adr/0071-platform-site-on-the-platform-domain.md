# ADR-0071: The platform's own site on the platform's domain

- Status: Accepted
- Date: 2026-11-02

## Context

The platform had no public site: its domain (`iftahonline.com`) went
through store-host resolution and answered "store not found". Merchants
need a page that says what افتح أونلاين is, what it costs and how to
start. The owner wants it professional, found by search engines, and
cited by AI assistants such as ChatGPT and Gemini.

The owner approved a plan and a mockup (S1, 2026-11-02):

- the site on the platform's domain, server-rendered like the
  storefront, with no runtime Node;
- plans and prices from billing: pounds in Egypt, dollars elsewhere;
- technical SEO and AI-assistant visibility built in;
- later stages: feature pages, an about page, an English site (S3); a
  console editor for articles, the FAQ and the site's settings (S4); the
  stores' own SEO.

## Decision

1. **A module, `site`, served by the storefront role.**
   - It renders templ pages, like the shop.
   - It registers host patterns on the storefront's mux
     (`server.MountPlatformSite`): `GET {domain}/` and
     `GET {domain}/assets/`. Go's `ServeMux` prefers a pattern with a
     host, so the domain never reaches store-host resolution, while
     every other host still does.
   - `www.{domain}` is sent to `{domain}` with a 301 that keeps the path
     and the query. The target is always the configured domain; only a
     numeric port (in development) is taken from the request.
2. **No copies.**
   - **Plans:** the site reads billing's `Showcase`
     (`billing.NewShowcase`, `PublicOffers`). It shares one `onSale`
     with the dashboard's `Offers`, so both price plans alike.
   - **Font:** the site uses the shop's brand typeface
     (`storefront.Shop.BrandFont`), whose files the shop already serves.
   - **Links:** sign-up and sign-in links go to the dashboard
     (`DASHBOARD_URL`, now also read by the storefront role).
3. **Prices by the visitor's country.**
   - The CDN's country header (`CF-IPCountry`) decides the currency:
     Egypt or an unknown country gets pounds, any other country dollars.
     A link switches the currency (`?currency=`, `nofollow`; the
     canonical URL stays `/pricing`).
   - **Until billing sells in dollars** (the Stripe work), a dollar
     visitor is told the dollar prices are coming and offered WhatsApp.
     Pounds are never shown in their place.
   - **Caching:** the pricing page is `private` and varies by the
     header. The home page shows the main market's prices, so the edge
     keeps it (`s-maxage=3600`).
   - **Billing reads:** plans read from billing are kept 5 minutes in
     memory, two entries at most.
4. **What crawlers and assistants read.**
   - Every page has its title and description, a canonical URL and
     Open Graph tags; the not-found page is `noindex`.
   - **schema.org JSON-LD:**
     - `Organization`: the brand's spelling variants, the support
       contact, and the parent company Codezilo Ltd with its London
       address;
     - `WebSite`;
     - `SoftwareApplication`, with an `Offer` per priced plan;
     - `FAQPage`;
     - `BreadcrumbList`.
   - **`robots.txt`** allows everything, naming the AI crawlers (GPTBot,
     OAI-SearchBot, Google-Extended, PerplexityBot, ClaudeBot and others),
     and points to `sitemap.xml`.
   - **`/llms.txt` and `/llms-full.txt`** (llmstxt.org) state, in plain
     facts, what the platform is, its pages, its plans in pounds and its
     answers.
5. **The copy speaks to merchants.**
   - It talks only about what a merchant gains, never about how the
     platform is built (the owner's rule).
   - The brand's spelling variants (افتح اونلاين، افتح اون لاين،
     iftahonline، eftahonline) appear once, naturally.
   - No made-up figures or reviews: example screens are labelled «مثال
     توضيحي». The recommended plan is «موصى بها», not a popularity claim.
6. **Strict and light pages.**
   - No script runs. The pricing cycle switch is radio buttons and CSS.
   - The stylesheet and the font faces are inlined under a hashed CSP
     (`default-src 'none'`); there are no style attributes.
   - Phones get a sticky «ابدأ مجانًا» bar with WhatsApp; wide screens
     get a floating WhatsApp button.

## Consequences

- **Positive:**
  - The domain has a fast site that crawlers read fully, with no new
    service, role or runtime.
  - Prices and plans follow the console.
- **Negative:**
  - **Static copy:** until S4 the copy, the FAQ and the support number
    live in the code.
  - **Dollar prices:** they wait for billing to sell in dollars.
  - **Share image:** the Open Graph image comes with the logo.
- **Outside the code** (`docs/go-live.md` §13):
  - DNS for the domain and `www` at the edge;
  - the CDN's country header;
  - Google Search Console and Bing Webmaster Tools, with the sitemap
    submitted;
  - the brand's social profiles;
  - a cadence of guides.

  No ranking can be promised.
