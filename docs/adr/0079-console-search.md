# ADR-0079: The console's search

- Status: Accepted
- Date: 2026-11-04

## Context

Support starts with one piece of information: a phone number, an invoice
number, a Paymob reference, a domain. Finding the store meant opening the
right page and filtering it. The owner approved one search across the
console on 2026-11-04, with two decisions:

- An exact email or phone finds the stores its owner owns, for the roles
  that read contacts (owners and operations).
- That lookup is recorded in the admin log, masked.

## Decision

### One endpoint, one result per kind

`GET /admin/v1/search?q=` (2 to 100 characters) answers every admin. It
returns at most 5 of each kind the role reads (ADR-0077); the other kinds
stay empty:

| Kind | Matches | Needs | How |
|---|---|---|---|
| Stores | the name, the slug's start, or a domain's start | `stores.read` | tenancy's directory, now also across `store_domains` |
| Owner's stores | an exact email, or the phone as E.164 in each of the platform's countries | `contacts.read` | identity's `app.owner_stores`, granted to `app_admin` only: store IDs, never the contact |
| Invoices | the number's start, or the store's or agency's name | `billing.read` | billing's console list |
| Payments | the gateway's exact reference | `billing.read` | billing's `app.billing_attempts_by_reference`, with a partial index on `reference` |
| Agencies | the name | `catalog.read` | billing's agencies |
| Admins | the name or the email | `team.manage` | the team |

- **Each source runs on its own** in parallel, with a 2-second timeout. A
  source that fails or runs late is named in `failed`, and the others still
  answer.
- **One query per kind:** no row is read one at a time. The owner's stores
  come back as IDs, and `Directory.Entries` reads them in one query.
- **Jump:** `jump` is the page of the one thing the query names exactly:
  - a full invoice number;
  - an ID, of a store or an invoice;
  - a store's primary domain.

  The console opens it on Enter.

### The owner lookup

- **Exact matches only:** the email is normalized, and the phone is read as
  E.164 in each region. A partial number finds nothing, so nobody can list
  merchants by trying digits.
- **Recorded first:** the lookup goes into the admin log before it runs, as
  `admin.searched_owner` with the query masked (the phone package's mask,
  or the email's first letter and domain). Other searches are not recorded.
- **No contact data in the result:** the result never carries the owner's
  contact. The store page shows it, to the same roles (ADR-0077).

### Not searched

- **Orders:** an order's code is per store, so it means nothing without the
  store.

## Consequences

- One field, and Ctrl+K anywhere in the console, reaches most things support
  needs.
- **Name search scans:** the store name and agency name parts are scans,
  like ADR-0076's invoice name search. That is fine at today's size. A
  trigram index is the fix if they grow slow.
