# ADR-0088: The merchant's country, and the US dollar

- Status: Accepted
- Date: 2026-11-05

## Context

The owner approved (2026-11-05) that payments follow the **merchant's
country**:
- **An Egyptian merchant** subscribes and pays the platform through
  Paymob, and his shoppers pay through Paymob or cash on delivery.
- **Every other merchant**, in the Gulf or anywhere else, subscribes and
  pays the platform in dollars through the platform's Stripe account.
  His shoppers pay through his own Stripe account or cash on delivery.
- **A country's own gateway** can be added later for that country's
  merchants.

A store recorded only its currency: EGP, SAR or AED. That said nothing of
a merchant outside the region, who also needs to sell in dollars (also
approved).

This ADR is the foundation, G1. Each later part has its own ADR:
- G2: the payment rules and the tax per country in the console;
- G3: billing in dollars;
- G4, G5: the Stripe adapters;
- G6: the gateway by country.

## Decision

1. **`stores.country`**, an ISO 3166-1 alpha-2 code
   (`platform/country.Normalize` on `golang.org/x/text`, which rejects
   macro-regions and private codes).
   - It is the currency's: EGP is Egypt, SAR Saudi Arabia, AED the UAE
     (`geo.CountryOf`). Sign-up may name it (`NewStore.country`,
     optional), but only as that one, until stores outside the region
     arrive (point 5).
   - **Only the console changes it**
     (`PUT /admin/v1/stores/{id}/country`, `stores.manage`), to a country
     the storefront delivers in: Egypt or a GCC country (`geo.Areas`). The change
     is recorded in the admin log first, with the countries from and to,
     and in the store's own log (`store.country_changed`).
   - Migration 00103 gives existing stores their currency's country. The
     column stays nullable (expand), and a store read without one gets
     its currency's.
2. **The dollar joins the platform's currencies** for the billing of
   merchants outside Egypt (G3).
   - `money.USD` (two decimals, "$").
   - Every currency check in the schema gains USD, as `NOT VALID`; a
     later migration validates them.
   - The sample products of the onboarding templates have dollar prices
     (the riyal's ÷ 3.75, to the half dollar).
   - The time zones a store may choose gain the main ones of the Levant,
     North Africa, Europe and North America.
3. **Billing keeps selling in EGP, SAR and AED** (`billing.Currencies`,
   `Billed`) until its dollar prices and Stripe arrive (G3). A store may
   sell in dollars before then.
4. **Egypt and the GCC first** (approved 2026-11-05). Addresses (the
   governorates of `platform/geo`), shoppers' phones (`platform/phone`)
   and shipping all serve Egypt and the GCC alone.
   - Until a later phase, "international stores", serves other
     countries, a store sells in EGP, SAR or AED
     (`domain.StoreCurrencies`), in its currency's country.
   - Sign-up refuses a dollar store and a country outside the region.
     Otherwise a store would open that cannot take an order.
5. **The contract.** Currencies are declared an extensible list
   (ADR-0032). The dollar's arrival in every response's currency list is
   accepted as its exact findings in `api/openapi-breaking-ignore.txt`.
   Clients show an amount in a currency they do not know with its ISO
   code (the dashboard's `currencySymbol`).

## Consequences

- **A store has a country** that later parts read to choose its gateways
  and its billing currency.
- **No store sells in dollars yet,** and no merchant outside Egypt and
  the GCC signs up: that is the "international stores" phase (addresses
  as free text where there is no list of areas, international phones,
  shipping zones of any country). The schema, the money package and the
  contract already take the dollar.
- **Screens.** The sign-up's country and currency, and the console's
  change of country, come with the approved mockups (G7).
