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
- **Mobile notifications** are Web Push from the console. They come in a
  follow-up on the same deliveries table (channel `push`).
- **WhatsApp:** later, by the owner's decision.

### In the console

`GET /admin/v1/alerts` lists, for the signed-in admin's role, the open
alerts and those resolved in the last 30 days. The server words each alert
(title, body, page), the same words as the email.

## Consequences

- An admin hears of a problem without opening the console. A short blip
  sends nothing.
- **What cannot alert:** a down database cannot raise an alert, because the
  check needs the database. An outside uptime monitor is P9's job.
- **Where it runs:** the check runs only when the worker has
  `ADMIN_DATABASE_URL`, like the console itself.
