# ADR-0034: The setup wizard and starter templates

- Status: Accepted
- Date: 2026-10-25

## Context

A merchant who signs up gets an empty store: no look, no products, no
shipping. The owner approved a setup wizard (2026-10-24, 2026-10-25),
with its mockup:

1. the account;
2. the store's name, address (`<slug>.iftahonline.com`) and country;
3. what the store sells: one of 8 activities;
4. the look (classic, modern, soft), a main colour and the logo;
5. quick shipping and cash-on-delivery settings (skippable);
6. "your store is ready".

After step 4 a **starter template** fills the store: the theme and home
page, categories, sample products marked as samples (shown, never
sellable, deletable in one go), shipping zones and draft policy pages.

Three questions follow. Where does the wizard's state live? How does
every new store get one? How is a template applied by one module when
the theme, catalog and shipping belong to others (CLAUDE.md rule 2)?

## Decision

1. **A new module, `onboarding`, owns the wizard.** Its table,
   `store_onboarding`, holds one row per store: the choice, where
   applying it stands (`none`, `preparing`, `ready`, `failed`), the
   template version applied, and when the wizard was closed.
2. **Sign-up opens the wizard in its own transaction.** Identity calls
   `onboarding.Starter.StartTx` inside the sign-up tenant transaction,
   as it calls `tenancy.CreateStoreTx`. Every store made from now on has
   a wizard the moment it exists. A domain event would leave a gap in
   which a just-signed-up merchant has no wizard yet. Stores made before
   the wizard have no row and read as `completed`, so they never see it.
3. **A choice is applied by a River job enqueued in the choice's
   transaction** (ADR-0003). The job applies the template through the
   `Templates` port, which `cmd/platform` builds from narrow interfaces
   in the storefront, catalog and shipping modules' `api.go` (the second
   step of this work). Applying must be idempotent: a job may run twice.
4. **The last choice wins.** `template_seq` counts choices, and each job
   carries its number. Jobs of one store hold an advisory transaction
   lock while they apply, so they never interleave; a job whose number is
   no longer current does nothing. Choosing does not take the lock, so
   the merchant never waits on a running job.
5. **Failure is visible.** A job retries 5 times; the last attempt
   records `failed` instead of retrying, and the wizard offers to try
   again (a new choice and a new job).
6. **The address is checked before sign-up.** `GET
   /v1/auth/slug-availability` (identity, public, 60 per 10 minutes per
   IP) asks tenancy, which applies the same slug rules as sign-up and
   reads the hosts directly, not through the resolver's caches. It is
   advisory: sign-up checks again.
7. **A look is a theme setting.** The three looks differ in layout, not
   only in colour, so the theme gains `look` (`classic`, `modern`,
   `soft`). The page body carries it as `data-look` and the stylesheet
   applies it: classic centres the logo on its own row, modern shows
   larger product cards, soft draws categories as circles on every
   screen. Stores without a look keep the original layout unchanged.
8. **Templates add no shipping zones** (the owner, 2026-10-25). The
   wizard's shipping step saves the merchant's own zones and price; a
   template cannot know them. A merchant who skips the step keeps the
   current rule (no zones: delivery everywhere, free), and the last
   screen says so.
9. **Templates are versioned data, not code paths.** Each template has a
   version, recorded with the store. Changing the template later touches
   only the theme and home page, never the merchant's products or
   shipping.

## Consequences

- The first step ships the module, the endpoints and the job with a
  no-op `Templates`: a choice becomes `ready` with no version. The
  templates themselves, the sample flag on products and their deletion
  come in the second step; the dashboard's wizard in the third.
- Identity depends on the onboarding module's `Starter`, as it depends
  on tenancy's `Service`; onboarding depends on neither.
- One more public, unauthenticated endpoint. It reveals only whether an
  address is in use, which anyone can learn by visiting it.
