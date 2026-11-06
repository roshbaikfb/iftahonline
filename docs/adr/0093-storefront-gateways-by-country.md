# ADR-0093: A store's gateways follow its country

- Status: Accepted
- Date: 2026-11-06

## Context

The approved rule (2026-11-05): a merchant's country decides the
gateway, not the shopper's. For storefronts:
- an Egyptian merchant's shoppers pay through Paymob, or cash on
  delivery;
- every other merchant's shoppers pay through Stripe (the merchant's own
  account, ADR-0092), or cash on delivery.

Another country's own gateway can be added later.

The console's payment rules (ADR-0089) already keep each country's
`store_gateways`:
- Egypt: `paymob`;
- the others (`ZZ`): `stripe`.

Until now the payments module offered every configured gateway to every
store.

## Decision

1. **A store lists and connects only its country's gateways.**
   - These are the `store_gateways` of the payment rule of its country,
     else the others' rule.
   - The fake gateway (development only) is always offered.
   - Connecting another gateway is a validation error on `gateway`
     (`not_offered`).
2. **The payments module asks through a function** (`Deps.Allowed`).
   cmd/platform adapts it from tenancy (the store's country) and
   billing's `PaymentRuleReader` (`billing.NewPaymentRules`, read only).
   Payments does not read billing's tables.
3. **A connection made before a rule changed keeps working.** The
   merchant's shoppers keep paying until the merchant disconnects. The
   rule decides what can be connected next, so a change in the console
   never stops a live store's card payments.
4. **Cash on delivery stays for every store.**

## Consequences

- **Gulf merchants see Stripe only**, and only once
  `PAYMENTS_STRIPE_ENABLED` is on (ADR-0092). Until then they cannot
  connect a card gateway, unless the console adds `paymob` to their
  country's rule (Paymob takes riyals and dirhams).
- **The rule is read on each list and each connection** (a small table,
  no cache): connecting is rare.
