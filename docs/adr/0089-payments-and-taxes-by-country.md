# ADR-0089: Payment rules and taxes by the merchant's country

- Status: Accepted
- Date: 2026-11-05

## Context

The approved rule (2026-11-05) makes payments follow the merchant's
country (ADR-0088):
- **Egypt:** the platform bills the merchant through Paymob, and the
  storefront takes cards through Paymob.
- **Everywhere else:** the platform bills through Stripe, in dollars,
  and the storefront takes cards through the merchant's own Stripe
  account.
- **Cash on delivery** is always offered.
- **A country's own gateway** can be added for it later.

The owner also asked for a tax per country set from the console, with a
rate for every other country. Until now, taxes were fixed for Egypt,
Saudi Arabia and the UAE (ADR-0049).

## Decision

1. **`payment_rules`**, platform data that only the console
   (`app_admin`) writes:
   - `country`: ISO 3166-1 alpha-2, or **ZZ** (CLDR's unknown region)
     for every other country;
   - `billing_gateway`: `paymob` or `stripe`;
   - `store_gateways`: those the storefront may take cards with. It may
     be empty, which leaves cash on delivery only.

   It is seeded with the approved rule: EG is Paymob/Paymob, ZZ is
   Stripe/Stripe. A rule names only the gateways the platform has
   (`domain.Gateways`), so adding a country's gateway means an adapter
   first, then the console.
2. **`PaymentRuleFor(country)`** is the country's own rule, else ZZ's.
   G3 reads it for billing, and G6 for the storefront.
3. **Taxes.**
   - `tax_rates` takes any country, plus a ZZ row (0% until the console
     sets it).
   - An invoice's tax is its account's country's rate, else ZZ's.
   - A change reaches the invoices written after it.
   - Accounts may be in any country.
4. **The console**, all recorded in the admin log first with a reason:

   | Endpoint | Permission |
   |---|---|
   | `GET /admin/v1/billing/payment-rules` | `billing.read` |
   | `PUT /admin/v1/billing/payment-rules/{country}` | `billing.manage` |
   | `POST /admin/v1/billing/payment-rules/{country}/remove` | `billing.manage` |
   | `GET /admin/v1/billing/taxes` | `billing.read` |
   | `PUT /admin/v1/billing/taxes/{country}` | `billing.manage` |
   | `POST /admin/v1/billing/taxes/{country}/remove` | `billing.manage` |

   - A removed row's country follows ZZ's.
   - ZZ's row stays (409 `others_stay`).
   - A tax rate is 0% to 50%.
5. **Migration 00104** adds the table and its seed, and the ZZ tax row.
   It replaces the country checks of `tax_rates` and `billing_accounts`
   with the ISO pattern, as `NOT VALID`.

## Consequences

- **Nothing changes how anyone is paid yet.** G3 and G6 read the rules.
- **The console's screens** for the rules and the taxes come with the
  approved mockups (G7).
