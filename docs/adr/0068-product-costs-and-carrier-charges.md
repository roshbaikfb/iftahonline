# ADR-0068: Product costs and carrier charges, for profit

- Status: Accepted
- Date: 2026-11-01

## Context

P8's profit dashboard subtracts four costs from revenue: product cost,
shipping, COD fees and returns. Nothing recorded the first. The others
are only partly known.

The owner approved (2026-11-01):

- **Product cost.** The merchant enters a cost per variant. Each order
  line keeps the cost of its moment.
- **Shipping and COD fees.** These start as the store's own estimates
  (8e-2). Once a carrier's payout arrives (ADR-0028), the real charge
  replaces the estimate.

Constraints:

- **No module reads another's tables.** The analytics module (8e-2) gets
  costs only through `orders` and `shipping` api.go.
- **Imports must not erase what merchants typed.** Product updates come
  from the editor, from CSV and from WooCommerce (ADR-0014). Only the
  editor knows about costs.

## Decision

1. **`variants.cost_minor`** (migration 00078): nullable, so null means
   "not known yet". It is in the variant's currency, between 0 and the
   price ceiling. Zero is a real cost.
   - **API.** It is a new optional field in the merchant API's variant
     input and output.
   - **Shoppers never see it.** The storefront's read side
     (`catalog.Storefront`) has no cost field.
   - **Omitting it keeps the known cost**, through a `-1` sentinel in the
     batch update. Imports and older clients therefore never erase it.
     The trade-off: a cost can be changed but not cleared.
   - **CSV imports** read a `cost` column, also `cost_price`, `التكلفة`
     or `سعر التكلفة`, with Arabic digits.
   - **Dashboard.** The variants grid has a "التكلفة" column.
2. **`order_items.unit_cost_minor`** (migration 00078).
   - At placement, each line copies its variant's cost:
     `SellableVariant.Cost` becomes `Line.UnitCost`. Null when unknown.
   - A later change of cost does not rewrite past orders' profit.
   - It stays inside Go (`orders.Order.Lines`). The order API does not
     expose it.
3. **`shipping.Charges.CarrierCharges(orderIDs)`** gives, per order with
   settled parcels, what the carrier kept:
   - for a delivered parcel, its COD less the payouts;
   - for a returned parcel, its negative payouts (return fees).
   - **One query**, over `shipments_store_order` and
     `cod_settlements_store_shipment`, for at most 500 orders.
   - Orders without settled parcels are missing. Their cost stays an
     estimate.

## Consequences

- **Positive:**
  - Profit can be computed per line, with the cost the merchant had then.
  - Imports and every existing client keep working, and keep the costs.
  - Carrier charges come from money that actually moved, not from rate
    cards.
- **Negative:**
  - **A cost cannot be cleared back to unknown** through the API. Setting
    another value is the only change.
  - **Orders placed before this change have no line costs.** The
    dashboard reports them as "cost not known" rather than guessing.
  - **Carrier charges lag the payouts,** which are weekly at best and
    imported by hand for Bosta.
