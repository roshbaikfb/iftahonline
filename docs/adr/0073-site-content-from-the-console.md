# ADR-0073: The site's content from the console

- Status: Accepted
- Date: 2026-11-02

## Context

The platform's site (ADR-0071, ADR-0072) kept all its words in code. The
owner wants to edit its content from the super-admin console without a
release: contact details, the FAQ, and later the founders, the team,
the guides and the founding-merchants program. The plan (S4) was
approved on 2026-11-02. S4a covers the contact details and the FAQ.

## Decision

1. **The site module owns its content, as platform data.**
   - The data lives in two tables, `site_settings` (one row) and
     `site_faqs`. They have no `store_id` and no RLS, like the ads
     (ADR-0062).
   - The console writes as `app_admin`; the storefront role reads as
     `app_user`, which cannot write.
2. **The console edits through `site.Console`.** The admin module calls
   it the same way it calls the ads:
   - every change needs a reason and goes in the admin log;
   - input is validated before it is recorded, so refused input leaves
     no entry;
   - edits are recorded first, and a new question is recorded once it
     has its ID.
3. **The site reads through a one-minute cache.**
   - One read is shared by every page.
   - When a read fails, the last content read is kept.
   - With no content read yet:
     - the home and FAQ pages answer 503, which is not cached;
     - other pages show the default WhatsApp number;
     - `llms.txt` leaves the FAQ out.
   - Pages stay 10 minutes at the edge (was an hour), so an edit shows
     within about ten minutes.
4. **What the console sets, and where it shows:**
   - **Contact details:**
     - two WhatsApp numbers in international format (any country);
     - an email address;
     - profiles on six networks, each link checked against that
       network's own hosts.

     They show in every page's footer, on the about page, and in the
     `Organization` data (`contactPoint`, `email`, `sameAs`).
   - **The company's Companies House number:** it links the public
     record on the about page and in the data.
   - **The FAQ:** in Arabic, with optional English; a question without
     English is left off the English site. Questions are ordered by
     position, some marked for the home page, at most 100.
5. **The migration seeds the FAQ the site shipped with,** so the site
   doesn't change on the day the migration runs.

## S4b-1: the about page and the site's identity

Added the same way (migration 00082):

- **New columns and tables:**
  - the founding story (Arabic and English), a YouTube video's ID, the
    logo and the share image, as columns of `site_settings`;
  - `site_people` (founders and team);
  - `site_testimonials` (merchants' real reviews only).
- **Images:**
  - The console uploads to `platform/site/uploads/` through a
    presigned POST.
  - The site module re-encodes each image into `platform/site/` (never
    SVG), as the ads do, and deletes the image it replaces or that
    belonged to a removed person.
  - Pages show images through signed imgproxy URLs. The policy's
    `img-src` adds the imgproxy origin and `https://i.ytimg.com`.
- **The video:**
  - The page shows its YouTube thumbnail linking to YouTube; nothing
    else of YouTube loads.
  - Links are parsed to an 11-character ID; the parser has a fuzz test.
- **Sections and data:**
  - Each section shows only once its content exists.
  - Founders go into the `Organization` data (`founder`), and the
    logo into `logo`.
  - Reviews get no `Review` data, because Google does not show reviews
    an organization publishes about itself.

## Consequences

- The FAQ's words are no longer in code. Tests use a fake content source
  holding the same ten questions.
- S4b and S4c add their content the same way: tables owned by `site`,
  methods on `site.Console`, and a screen in the console.
- Purging Cloudflare's cache on save is left for later. It needs an API
  token.
