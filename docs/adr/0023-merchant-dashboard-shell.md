# ADR-0023: The merchant dashboard's shell, and browser tests on the real API

- Status: Accepted
- Date: 2026-10-23

## Context

Until P4 the dashboard (React, Vite, TypeScript strict, the generated
API client) showed only the API's build info. P4 needs merchants to:

- sign in;
- list their orders with filters;
- confirm pending orders in bulk;
- move an order along;
- export orders as CSV.

The API already offers all of this (ADR-0005, ADR-0021). Two choices had
to be made:

- **The client side:** routing, the session, and data loading.
- **Browser tests:** how to test in a browser against something real.
  The old smoke test ran the API with no database, which can only check
  `/v1/meta/version`.

## Decision

1. **Routing: react-router.**
   - It is the only new runtime dependency, at 8.4.0 (exact pin, older
     than two weeks).
   - Routes:
     - `/`: the first store's orders;
     - `/stores/:storeId/orders`: the list, with its filters in the
       query string, so a filtered list can be shared and reloaded;
     - `/stores/:storeId/orders/:orderId`: one order.
   - No data or state library: pages fetch through the generated client.
2. **The session stays the HttpOnly `__Host-sid` cookie** (ADR-0005).
   - The dashboard never sees a token.
   - `GET /v1/me` tells who is signed in (401: show the login page).
   - Sign-in handles the TOTP step (202 then `POST /v1/auth/mfa`).
   - State-changing calls are same-origin `fetch`es, which pass the
     API's CSRF check (Sec-Fetch-Site).
   - The CSV export is a plain link (a GET with the cookie).
3. **The UI mirrors the state machine only to choose buttons.** The server
   decides every move and its permission. A move sends the version the
   page read, so a concurrent change is refused (409) rather than
   overwritten.
4. **Effects set state only when their request answers** (React 19's
   rule). User actions set "loading" themselves.
5. **Browser tests run on the real API** (`internal/tools/dashdemo`).
   - It starts throwaway PostgreSQL and Redis (`testinfra`) and runs the
     real `platform api` binary on them.
   - It seeds a merchant through the API: sign-up, a product, orders,
     one confirmed.
   - Only then does it serve on the port Playwright waits for, so the
     data is ready before any test starts.
   - The browser uses `http://localhost`. A `__Host-` Secure cookie is
     accepted there without TLS, just as production uses HTTPS.
   - The journey:
     1. sign in;
     2. bulk confirm;
     3. filter by phone;
     4. pack and ship from the order page;
     5. export the CSV;
     6. sign out.
   - **No console errors are allowed**, except the expected 401 of a
     visitor's `GET /v1/me`.

## Consequences

- **Positive:**
  - The dashboard is tested end to end against the same binary and
    database schema as production.
  - A contract change that breaks a screen fails `make ci`.
- **Negative:**
  - **`make ci` starts two more containers** for the dashboard (about
    10 s).
  - **The store switcher shows store IDs** for stores other than the
    current one: `GET /v1/me` returns memberships without names. A names
    field can come with the store settings screens.
  - **Partial UI.** Only orders exist in the UI so far; catalog,
    customers and settings screens come in later phases (the API serves
    them already).
