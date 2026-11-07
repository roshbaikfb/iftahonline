# ADR-0095: Support's risky changes to a merchant account wait 24 hours

- Status: Accepted
- Date: 2026-11-07

## Context

The 2026-11-07 console review found a gap. Platform support had no
way to help a merchant locked out of their own account.

A member with two-step verification who loses both the phone and the
recovery codes cannot sign in again. D3 made "forgot password" keep the
second factor on purpose, so a stolen mailbox alone never opens an
account.

The fix is a console action that removes a member's second factor.
That action is also the easiest thing for an attacker to talk support
into doing. A caller can claim to be the merchant and give
believable details.

The owner chose the safer of two designs (2026-11-07):

- **Rejected:** the change is made at once.
- **Chosen:** the change waits 24 hours. The real account holder and
  the store's owner are emailed at once, with a link that cancels it.

The same applies to moving a store's ownership from the console (C2).

## Decision

1. **`support_requests`** is a tenant table (store_id, RLS) holding:
   - the kind: `mfa_reset`, or `ownership_transfer` from C2;
   - the member;
   - the admin who asked;
   - the reason;
   - `effective_at`, set to 24 hours after the request;
   - how the request closed: cancelled (by `link` or `console`) or
     done.

   Partial unique indexes keep one open request of a kind per member,
   and one open transfer per store.
2. **The request, its emails and its execution are one transaction.**
   - The request is inserted.
   - Cancel links go to the member and, when the owner is someone
     else, to the owner.
   - A River job `identity.support.execute` is scheduled at
     `effective_at`.

   The job locks the request. It does nothing when the request was
   cancelled, and refuses to run before its time.
3. **Cancel links are `user_tokens` of purpose `support_cancel`.**
   - Each link names its request and store, because the link page opens
     without a session.
   - Only a hash is stored. The link itself waits sealed in Redis for
     its email job, as D3's links do.
   - A link works until `effective_at`.
   - Closing the request, by cancelling or by running it, closes every
     link it sent.
4. **Cancelling from the link tells the admin who asked.**
   - Identity cannot read the admins' table. The worker therefore gets
     an `AdminNotifier`, wired in `cmd/platform` to the console's
     mailer.
   - The store's activity log records the cancellation, as it records
     every step.
5. **Protective actions stay immediate.** Disabling an account and
   signing it out everywhere only take access away. Disabling applies
   in every store the account works on, because accounts are
   platform-wide. The console shows the account's other stores before
   it disables one, through `app.member_stores`, a SECURITY DEFINER
   function granted to `app_admin` only.
6. **The console reaches identity through `admin.StoreSupport`.** This
   follows the Impersonator pattern of ADR-0055, so neither module
   imports the other. Each action is recorded in the admin log, with
   its reason, before identity is called. Identity then records it in
   the store's log, with `support_admin` in the metadata.

## Consequences

- A merchant who truly lost their phone waits a day. Support tells them
  so, and the console shows the request's time.
- An impersonator's request reaches the real merchant's mailbox first.
  This fails only if the attacker controls that mailbox as well. In
  that case, `forgot password` plus this request would open the
  account, so support's identity checklist (the approved dialog)
  remains the first wall.
- `user_tokens` gains two nullable columns and a widened check. The
  migration only expands the schema.
