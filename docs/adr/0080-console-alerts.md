# ADR-0080: The console's alerts

- Status: Accepted
- Date: 2026-11-04

## Context

The console's home showed problems only to an admin who opened it. The
owner approved alerts on 2026-11-04, with three decisions:

- Email and mobile notifications now, WhatsApp later.
- Each alert goes to the roles it concerns.
- Recovery is announced for red alerts only.

## Decision

### What is watched

`domain.Signals.Read` turns one check into a reading per kind. A section
the check could not read is *unknown*: it neither opens nor resolves
anything.

| Kind | Fires when | Severity | Told to |
|---|---|---|---|
| `duplicates` | a duplicate payment waits for a refund | red | owners, finance |
| `discarded` | jobs were discarded in the last 24 hours | red | owners, operations |
| `slow` | the oldest waiting job is older than 10 minutes | red | owners, operations |
| `redis` | Redis does not answer | red | owners |
| `overdue` | invoices are past due | amber | owners, finance |
| `held` | stores are held for an unpaid invoice | amber | owners, finance |
| `past_due` | subscriptions are in their grace period | amber | owners, finance |

### Opening and resolving, without flapping

A River periodic job in the worker checks every 5 minutes, on the console's
pool. `domain.Step` keeps a streak per kind (`admin_alert_state`, migration
00094):

- An alert **opens** after **two bad checks in a row**.
- It **resolves** after **two clean checks in a row**.
- One check alone changes nothing.

`admin_alerts` keeps each opening with its latest number. A partial unique
index allows one open alert per kind. A check locks the open alerts in its
transaction.

### Notices

- **Who and when:** each active admin whose role covers the kind is told
  once when the alert opens. For a red alert, the same admins are told
  once more when it resolves.
- **Exactly once:** `admin_alert_deliveries` records each notice by alert,
  admin, event and channel, so a notice is never sent twice.
- **Failures:** a notice that fails is tried again at the next check. A
  notice not sent within a day is dropped.
- **Email:** sent through `platform/mailer` from the worker, never from a
  request. It carries the alert's words and the console link that acts on
  it. Each admin can turn alert emails off (`platform_admins.alert_email`,
  `PUT /admin/v1/alerts/email`).
- **Mobile notifications** are Web Push (RFC 8030/8291, VAPID per RFC
  8292), recorded on the same deliveries table (channel `push`):
  - An admin turns them on per device from the console
    (`POST /admin/v1/push/subscriptions`); any admin may, and hears only
    of their role's kinds. A device is stored in `admin_push_subscriptions`.
  - The endpoint is the browser's word, so it is accepted and reached only
    on the big push services' hosts (Google, Mozilla, Apple, Microsoft),
    over https on 443, through `platform/safehttp`, without redirects.
  - The worker sends from `VAPID_PRIVATE_KEY`; the api hands the console
    `VAPID_PUBLIC_KEY`. Without the keys, push is off and only email goes.
  - A notice counts as sent when any of the admin's devices took it. A
    device the push service no longer knows (404/410) is forgotten.
  - Red alerts are sent with high urgency. The notification carries the
    alert's title, body and console page, nothing else.
  - In the browser, the service worker `/console-sw.js` (scope `/console`)
    shows it, tagged by the alert so its resolution replaces it, and a
    click opens the console on that page (a path inside the console
    only). It sits at the root because a worker's scope cannot reach above
    its own folder.
  - iPhones take web notifications only from a web app on the home
    screen (iOS 16.4+), so the console has a manifest
    (`/console.webmanifest`, standalone) and icons, and the alerts page
    says how to add it.
- **WhatsApp:** later, by the owner's decision.

### In the console

`GET /admin/v1/alerts` lists, for the signed-in admin's role, the open
alerts and those resolved in the last 30 days. The server words each alert
(title, body, page), the same words as the email.

- **`/console/alerts`** shows how the alerts reach the admin (the email
  switch, and notifications on this device), then the open alerts, each
  with its page, then the resolved ones.
- **The bell** in the menu and in the phone's top bar shows the open
  count, read every minute while the tab is seen.
- **The home's «يحتاج انتباهك»** shows the same open alerts, read with
  the health check every minute: one source for what needs the admin, by
  role. When the alerts cannot be read (a down database, for one), it says
  so in a red card instead of «all is well».

## Consequences

- An admin hears of a problem without opening the console. A short blip
  sends nothing.
- **What cannot alert:** a down database cannot raise an alert, because the
  check needs the database. An outside uptime monitor is P9's job.
- **Where it runs:** the check runs only when the worker has
  `ADMIN_DATABASE_URL`, like the console itself.
