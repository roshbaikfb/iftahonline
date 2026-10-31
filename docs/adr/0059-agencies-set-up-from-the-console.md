# ADR-0059: Agencies set up from the console

- Status: Accepted
- Date: 2026-10-31

## Context

The P7 spec asks for agency accounts: many stores, one invoice. ADR-0049
already invoices an agency once. It bills its plan once and lists each
store's fees. But nothing created an agency or moved stores into one.
A store gets its own account the first time it has something to bill, a
store is in one account at a time, and invoices are append-only.

The user approved three rules on 2026-10-31:
1. **Joining:** a store with its own account may join an agency if that
   account has no open invoice. Its old invoices stay there, and the
   next ones are the agency's.
2. **One shared period.** The first store's period runs from now to the
   first of next month (UTC). A store that joins later takes the
   agency's plan version, status and period. The agency's price is
   flat, so joining mid-period costs nothing more, and the agency is
   billed once a month.
3. **Leaving:** a store that leaves goes to the free plan. Its next
   invoice opens its own account.

## Decision

1. **The account names its plan.** Migration 00066 adds
   `billing_accounts.plan_code`, which an agency must have (`CHECK … NOT
   VALID`). Its stores renew into that code's newest version (ADR-0056).
2. **Membership moves; invoices stay.**
   - **Permission.** `app_user` may now delete a
     `billing_account_stores` row. Invoices and their lines stay
     append-only.
   - **One transaction.** Joining moves the membership, sets the account
     on the subscription, writes the subscription and publishes
     `SubscriptionChanged`. The transaction switches walls with
     `set_config(..., true)`:
     - the store's own wall;
     - its old account's wall, to check for an open invoice and remove
       the membership;
     - the agency's wall, to lock the agency (`FOR UPDATE`, so stores join
       and leave one at a time) and add the membership;
     - another member's wall, to read the period to share.
3. **What a subscription becomes.** The pure domain function
   `domain.JoinAgency` computes the subscription after joining, and
   `domain.LeaveAgency` after leaving. A joining store is held or
   released for billing as the agency is.
   - **`PutSubscription`:** writes a subscription whole and creates it
     when the store had none. A new store has no row, which means the
     free plan.
   - **Billed renewals:** it forgets a billed renewal. The old account's
     invoices were all settled first, so nothing is lost.
4. **Listing agencies.** `app.agency_accounts()` lists agencies for the
   console: names, currency, plan code and the number of stores, never
   their members' data. It reads as the owner, through new read-only
   `billing_lookup` policies on the account tables.
5. **The console** (`/admin/v1/agencies…`):
   - **Endpoints:** list, create, view (with its store IDs), add a
     store, remove a store.
   - **Audit:** joining and leaving are written to the admin log first
     (`store.agency_joined`, `store.agency_left`). Creation is written
     once billing has given the agency its ID (`agency.created`); a
     refused creation leaves nothing to record.
   - **Single currency:** the store's currency must be the agency's
     (approved 2026-10-30).

## Consequences

- **A free first period.** The first store's partial month is not
  billed. The first invoice is the renewal at the end of that month.
- **A late joiner shares the status.** A store that joins a past-due
  agency becomes past due with it. Paying the agency's invoice settles
  every member (ADR-0051).
- **No merging.** Moving a store between two agencies is "leave, then
  join". Moving a whole agency's stores into another is not supported.
