# ADR-0090: Billing in dollars outside Egypt

- Status: Accepted
- Date: 2026-11-05

## Context

The approved rule (2026-11-05, ADR-0088):
- **An Egyptian merchant** pays the platform in pounds through Paymob.
- **Every other merchant** pays it in dollars through the platform's
  Stripe account.

Until now billing sold in the store's own currency: EGP, SAR or AED.

The owner also approved the pay-as-you-sell fee outside Egypt: a
percentage of the order **and** a fixed amount per order (as EasyOrders'
$0.04), each set from the console. The percentage of an order in another
currency converts at a rate the console sets.

This is G3. The Stripe adapters (G4, G5) and the gateway by country (G6)
follow.

## Decision

1. **The billing currency follows the merchant's country**
   (`domain.BillingCurrency`): the pound in Egypt, the dollar elsewhere.
   - An account takes it when it opens (`storeAccountTx`, the one place
     a store's account is opened), with the store's country for its tax
     (ADR-0089).
   - An account keeps its currency.
   - **Older accounts** opened in riyals or dirhams stay in them
     (`domain.Legacy`). Nothing new is sold in those currencies: plans,
     agencies and the console's offers are in EGP and USD
     (`domain.Currencies`). An account is billed in any of the four
     (`domain.Billed`).
2. **Prices.**
   - A paid plan needs a monthly price in EGP and USD.
   - Pay as you sell needs a fee in EGP and USD.
   - Prices and fees in the legacy currencies are optional, kept for the
     accounts still billed in them.
   - Migration 00105 prices every plan priced in riyals in dollars too,
     at 3.75, to the cent, so that Gulf merchants have offers before the
     console sets its own.
3. **The fee** is the rate of the goods plus `fixed_minor` per delivered
   order, then the bounds, and never more than the goods.
4. **Exchange rates** (`fx_rates`): each store currency's units per
   dollar, times 10^6. The riyal (3.75) and the dirham (3.6725) are
   seeded at their pegs; the pound is the console's to set.
   - A delivered order's goods in another currency than its account's
     are converted at the rate between the two (`domain.RateBetween`),
     rounded half up (`money.Convert`).
   - The fee is then the rule's on the converted goods.
   - The fee keeps the order's currency, its goods and the rate, so an
     invoice can be explained later and a new rate changes no past fee.
   - With no rate the job fails and retries: it never charges at par.
5. **The dollar's collection minimum** is $10. Saving the settings
   replaces the minimums sent and keeps the others, so a console that
   knows fewer currencies changes none of the others.
6. **Agencies** are created in their country's currency, with the
   country: EGP in Egypt, USD elsewhere. A store joins an agency only in
   the currency it is billed in.
7. **The console:**

   | Endpoint | Permission |
   |---|---|
   | `GET /admin/v1/billing/fx-rates` | `billing.read` |
   | `PUT /admin/v1/billing/fx-rates/{currency}` | `billing.manage`, a reason, recorded first with the rate it replaces |

   The plan editor gains the dollar and the fixed amount. Its full
   design, and the merchant's screens, come with the approved mockups
   (G7).
8. **The contract** only gains:
   - optional request fields (`fixed_minor`, `country`);
   - wider arrays (four fees, one to four minimums);
   - new response fields;
   - the two endpoints.

## Consequences

- **Gulf merchants who open an account from now on are billed in
  dollars.** Until the Stripe billing adapter (G4), Paymob cannot charge
  them. Gulf billing must not be switched on before G4 ships. It is not
  on yet (the pre-launch checklist).
- **A store's country is fixed for billing once its account is open.** A
  later change of country (the console's, ADR-0088) does not move an
  open account to another currency.
- **The riyal and the dirham stay readable** for older accounts. A later
  contract migration can remove them once no account is billed in them.
