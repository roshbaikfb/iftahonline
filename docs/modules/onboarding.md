# Module: onboarding

The onboarding module owns the new store's **setup wizard**: what the
merchant sells (the activity), the storefront's look and main colour,
and the **starter template** applied from that choice. Design:
[ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md).

## Public API (`internal/modules/onboarding/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount`, `Module.RegisterWorkers` | Wiring, the merchant HTTP API (behind identity's membership middleware), and the apply job's worker |
| `Module.Starter()` → `Starter` | What sign-up (identity) uses: `StartTx(ctx, tx)` opens the wizard of the new store in sign-up's transaction |
| `Choice`, `Activity`, `Look` | The merchant's pick |
| `Deps.Catalog`, `Deps.Storefront`, `Deps.Currency` | The worker's way to apply starter templates: `catalog.Starter`, `storefront.Starter` and the store's currency. The api role passes none and only enqueues |

## Starter templates

`internal/templates` holds one data file per activity (`data/<activity>.json`,
read strictly, fuzzed) and the pictures its products share
(`images/*.jpg`, rendered from `scripts/starter-images/*.svg` by
`node scripts/build-starter-images.mjs`). Each template has 3 categories
and 6 products priced in EGP, SAR and AED, slugs starting `sample-`.
Applying one, in order:

1. the categories (`catalog.Starter.EnsureCategory`);
2. the sample products, each with its category's picture
   (`EnsureSampleProduct`: marked samples, never sellable);
3. the theme and home page of the chosen look and colour
   (`storefront.Starter.ApplyLook`): classic (IBM Plex, small radius, a
   centred hero with the first product's picture), modern (Cairo, medium
   radius, the heading beside the picture, 6 larger cards), soft
   (Tajawal, large radius, a 6% tint of the colour, round categories
   first);
4. draft refund, shipping and privacy policies, unless the merchant saved
   them.

Every step is idempotent by slug. Templates add no shipping zones.

## Events

None. The apply job is a River job enqueued with the choice
(`onboarding.apply_template`, ADR-0003).

## Tables

| Table | Notes |
|---|---|
| `store_onboarding` | Tenant table under RLS, one row per store made since the wizard (PK `store_id`). Stores made before it have no row and read as `completed`. `template_seq` counts choices; see the rules below |

## HTTP API (tag `onboarding`)

| Operation | Permission |
|---|---|
| `getOnboarding` | `store.read` |
| `chooseOnboardingTemplate` (202; 409 `onboarding_completed` once closed) | `store.update` |
| `completeOnboarding` (idempotent) | `store.update` |

## Rules worth knowing

- **The last choice wins.** Each choice increments `template_seq` and
  enqueues a job carrying the new value. Jobs of one store take an
  advisory transaction lock, so they run one at a time; a job whose
  value is no longer current does nothing. Choosing never waits on the
  lock.
- **Failures are shown, not hidden.** A job retries 5 times; the last
  attempt records `failed`, and the wizard offers to try again (choosing
  again enqueues a new job).
- **Audit.** `onboarding.template_chosen` (activity, look, colour) and
  `onboarding.completed`.
