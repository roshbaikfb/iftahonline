# ADR-0028: Cash-on-delivery reconciliation

- Status: Accepted
- Date: 2026-10-23

## Context

In cash-on-delivery commerce the carrier collects the merchant's money
at the door and pays it out later (Bosta: a daily "collected cash cycle"
into a wallet, and weekly cash-outs). Merchants lose track of what is
still owed, and of parcels paid short or not at all.

P5 asks for:

- COD reconciliation: expected vs settled per shipment;
- a report of the money still held by the carrier.

Constraints:

- **Bosta has no settlements API** (ADR-0026). Its statements are
  downloads from its dashboard, and we have no sample of their exact
  columns.
- **Other carriers may have an API.** The `Carrier` contract has
  `CODSettlements`, and the fake carrier implements it.

## Decision

1. **Settlement lines** (`cod_settlements`, migration 00033):
   - One line is one payout for one parcel.
   - **Kept as lines, not as a shipment column.** Payouts can be partial,
     repeated or corrected, and a line may name a parcel we do not know.
   - **Matching.** Each line is matched to a shipment by tracking number
     within the store and carrier, when it is written. An unmatched line
     is kept with `shipment_id` null.
   - **Idempotent.** A line is unique per connection, tracking number and
     reference (the payout's id), so importing or pulling the same
     payouts again adds nothing (tested).
   - Negative amounts (adjustments) are allowed.
2. **Two ways in:**
   - **Statements** (`importCODStatement`, `finance.manage`). A CSV body
     for one connection, active or disconnected (statements arrive after
     a disconnect). The parser:
     - accepts UTF-8 with or without BOM, comma or semicolon separators,
       at most 1 MiB (the platform's request limit) and 20,000 lines;
     - finds columns by name: our template (`tracking_number`, `amount`,
       `settled_at`, `reference`) plus common Arabic and English names
       from carriers' exports;
     - reads Arabic digits, amounts with thousands separators or a
       trailing currency (`money.Parse`, with no silent rounding), and
       ISO or day-first dates in the store's time zone;
     - skips bad lines and reports them (at most 100);
     - is fuzzed (`FuzzParseStatement`).

     The import is synchronous: no external call, one transaction, one
     batched round trip.
   - **Pulls.** Each connection gets a `pull_settlements` job when it
     connects. It asks for the last 7 days of payouts, then snoozes 24 h.
     It ends when the carrier has no settlements API (`ErrUnsupported`:
     Bosta stops at once), when its key fails, or when the connection is
     disconnected.
3. **The report** (`getCODReport`, `finance.read`):
   - **Scope.** Parcels delivered in [from, to), 30 days by default and
     at most a year.
   - **Per carrier account:**
     - delivered COD parcels;
     - expected (their COD);
     - settled (the sum of their lines);
     - held (expected − settled, never below zero per parcel), split by
       delivery age into 0–7, 8–14 and 15+ days;
     - parcels paid out more than their COD.
   - **Across the store:** the paid-out lines that match no parcel, and
     their total.
   - **`listHeldCOD`** goes parcel by parcel, oldest delivery first.
   - **Performance.** The queries run on store_id indexes (checked as
     app_user under generic plans). The settled sum per parcel is a
     LATERAL lookup on `(store_id, shipment_id)`.
4. **Permissions.** `finance.read` and `finance.manage` belong to the
   owner and the manager. Money is not support's or shipping's business.

## Consequences

- **Positive:**
  - Merchants see how much each carrier still holds and since when,
    which parcels are short or over, and which payouts point at nothing.
  - A carrier with an API needs no manual step.
- **Negative:**
  - **Bosta's real export may use column names we do not know.** The
    template columns always work; a new alias is one line in the parser
    once a real statement is seen.
  - **A statement over 1 MiB must be split.** The import could become a
    job behind an upload (like catalog imports) if merchants need more.
  - **Expected is the shipment's COD at shipping time.** An order whose
    amount changed after shipping (none can today) would need its
    shipment updated too.
