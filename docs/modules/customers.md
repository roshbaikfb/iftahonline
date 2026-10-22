# Module: customers

The customers module owns a store's **customers**: one per phone number,
created by their first order, with their latest name, order count and the
store's **block list**. Design:
[ADR-0020](../adr/0020-customers-order-stock-and-monthly-partitions.md).

## Public API (`internal/modules/customers/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount` | Wiring, and the merchant HTTP API (behind identity's membership middleware) |
| `Module.Customers()` → `Customers` | What orders use: `Blocked(ctx, phone)` before any work, `RecordOrderTx(ctx, tx, phone, name)` in the order's transaction |
| `Customer` | The customer DTO |
| `ErrCustomerBlocked`, `ErrCustomerNotFound` | Errors; shoppers must only ever see a generic refusal for the first |
| `RegionSource` | The store's phone region; `cmd/platform` derives it from the store's currency (`platform/geo`) |

## Events

None yet. Order history is read from the orders module (filter by
customer).

## Tables

| Table | Notes |
|---|---|
| `customers` | Tenant table under RLS. Unique `(store_id, phone)` (E.164, natural key). `name_key` is the name in search form. `blocked_at` and `block_reason` form the block list. |

## HTTP API (tag `customers`)

| Operation | Permission |
|---|---|
| `listCustomers` (`q` = phone or name prefix, `blocked`, cursor) | `customers.read` |
| `getCustomer` | `customers.read` |
| `blockPhone` (a number that may never have ordered) | `customers.manage` |
| `blockCustomer`, `unblockCustomer` (idempotent) | `customers.manage` |

Owner, manager and support hold both permissions; shipping holds neither.

## Rules worth knowing

- **Phones.** Numbers typed without a country code are read in the
  store's country. Responses carry the full number (the merchant calls
  it); logs and audit entries never do.
- **Audit.** `customer.blocked` and `customer.unblocked` name the
  customer by ID only. The reason is not logged either: it is free text
  about a person.
- **Lists.** Newest first. The keyset bound is always an index condition,
  so deep pages cost the same as the first.
  `TestPhoneLookupUsesIndexUnderRLS` checks this on 20,000 customers,
  together with the phone lookup that runs before every order.
- **Fuzzing.** `domain`: `FuzzResolveQuery`. Any text resolves to a
  phone, a LIKE-safe name prefix, or a validation error.
