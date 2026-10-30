# ADR-0055: Time-limited, audited impersonation

- Status: Accepted
- Date: 2026-10-30

## Context

The P7 spec gives the super admin "time-limited impersonation with a
reason and an audit trail". Its acceptance test is "impersonation always
audited and expires". Support needs to see a store the way its owner
sees it in the dashboard. Doing that must never become a quiet way to
act as the merchant.

The console (ADR-0005 §8) runs on its own host, with its own sessions
and the `app_admin` role. The dashboard runs on `app_user` with merchant
sessions. Identity owns the merchant sessions. Other modules never
import identity, and admin does not either.

## Decision

1. **A support session is a real merchant session of the store's
   owner**, marked in `user_sessions` (migration 00062) with three
   columns:
   - `impersonator_id`: the admin;
   - `scope_store_id`: the one store it may open;
   - `read_only`.

   A CHECK makes every impersonation session scoped to a store. Every
   module's handlers, policy and RLS then work unchanged.

2. **Bounds.**
   - **Lifetime:** 5 to 60 minutes; the console defaults to 30.
   - **No extension:** the session is never touched, so use never
     extends it.
   - **Expiry:** it stops working at `expires_at` like any session, and
     the dashboard cookie's Max-Age ends with it.
   - **Read-only by default:** the admin asks for `write` explicitly.

3. **Scope.** The Scope middleware enforces it:
   - **Other stores:** any store but `scope_store_id` is 404, even one
     where the owner is a member.
   - **Read-only:** a read-only session gets 403 for any method that is
     not GET, HEAD or OPTIONS.
   - **Outside a store:** a session may read (`/v1/me`, which then lists
     only its store and an `impersonation` object for a banner) and use
     `/v1/auth/*`. It can never change the account, such as the phone,
     the MFA settings, its sessions or invitations.

4. **Always audited, in three places.**
   - **The admin log, first.** `store.impersonated` records the reason,
     the minutes and `write`, as suspension does (`setStatus`). It is
     written before identity is called, so a failed attempt is still
     visible.
   - **The store's log, at the start.** `impersonation.started` is
     written in the same transaction that inserts the session, with
     `impersonated_by`, the reason, `read_only` and `expires_at`.
   - **The store's log, on every request.** `impersonation.request` is
     written for every request the session makes, served or refused. It
     keeps the method and the route pattern, never query strings or
     bodies. A request that cannot be audited is not served.

   The recorder also adds `impersonated_by` to every entry any module
   writes during the session. The actor stays the owner, so the store's
   staff see who acted on whose behalf.

5. **Handing over the session.**
   - **The code.** The console's `POST
     /admin/v1/stores/{id}/impersonate` returns a one-time code (256
     bits, valid for 60 s) and a dashboard URL that carries the code in
     the fragment, which browsers never send to a server.
   - **Its storage.** The code is kept in Redis, sealed, under its
     SHA-256.
   - **The exchange.** The dashboard posts the code to `POST
     /v1/auth/impersonation`, which deletes it as it reads it (GETDEL)
     and sets `__Host-sid`. That route is throttled per IP.
   - **Why a code.** The session token itself never appears in the
     console, in a URL or in any log.

6. **Wiring.** `admin.Deps.Impersonate` is a function type. cmd/platform
   adapts `identity.Module.Impersonate` to it, so neither module imports
   the other.

## Consequences

- **The owner can see and end it.** The session appears in the owner's
  session list (user agent `impersonation`), and the owner can revoke it.
- **Write sessions act as the owner.** A `write` session can do anything
  the owner can in that store. This is why it must be asked for, and why
  every request is logged.
- **Many audit rows.** One row per request is a lot for a busy session.
  Support sessions are rare and short, so this is acceptable.
- **No dashboard page yet.** The dashboard's `/impersonate` page and its
  banner come in 7f, with the console SPA. Until then the code can be
  exchanged by hand.
