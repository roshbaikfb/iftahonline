# ADR-0078: The console's team and invite links

- Status: Accepted
- Date: 2026-11-04

## Context

ADR-0077 gave each admin a role, but admins could still be added only from
the CLI, and roles and access changed only in SQL. The owner approved the
team page and its mockup on 2026-11-03. The invite decision: a one-time
link, so the invitee sets the password and scans the authenticator
themselves, and the owner never knows either.

## Decision

### Invites

- **Creating one:** an owner (`team.manage`) invites an email, a name and a
  role, with a reason.
- **The invite row:** `admin_invites` (migration 00092) keeps:
  - the SHA-256 of the link's token (the token is returned once);
  - the TOTP seed, sealed for the admin it will create (AAD
    `admin-totp:<admin_id>`, the same as a created admin's);
  - an expiry of 72 hours.
- **One open invite at a time:** at most one per admin, and one per email
  for new admins.
- **The link:** `/console/invite#<token>`. The token sits in the fragment,
  so it is never sent in a request line or logged; the page posts it in a
  body.
- **Lookup** (`POST /admin/v1/invites/lookup`, no session) shows:
  - who invited them and to which role;
  - the seed as text, as `otpauth://` and as a QR code. The server draws
    the QR as SVG with `rsc.io/qr`, so the seed never goes to a third party.
- **Accept** (`POST /admin/v1/invites/accept`, no session):
  - The invitee gives a password (14 characters or more) and a current code
    from that seed.
  - In one transaction: the invite is spent, the admin is created with the
    sealed seed, and the code's step is claimed.
  - The new admin is then signed in.
- **What the public routes leak:** an unknown, used, cancelled or expired
  link is the same 404, so nothing is revealed. Lookups and accepts share
  the sign-in throttle per address.
- **Renewing** a link closes the old one and gives a new token and a new
  seed. **Cancelling** a link closes it.

### Team changes

- **What an owner can change** for another admin:
  - the role;
  - disable, which ends its sessions at once;
  - enable again;
  - reset, for a lost phone: the admin is disabled and signed out, and a
    reset link (an invite with purpose `reset`) sets a new password and seed
    and enables it again.
- **Nobody acts on themselves** (409 `not_yourself`).
- **The last owner:** a change that would leave no active owner is refused
  (409 `last_owner`). The active owners are locked (`FOR UPDATE`) in the
  same transaction, so two owners cannot demote each other at once.
  `domain.LeavesAnOwner` holds the rule.
- **Audit first:** every change is recorded in the admin log before it is
  made, with its reason: `admin.invited`, `admin.invite_renewed`,
  `admin.invite_cancelled`, `admin.role_changed` (from and to),
  `admin.disabled`, `admin.enabled` and `admin.reset`. The invitee's own
  `admin.joined` or `admin.reset_completed` names the new admin as the
  actor.

## Consequences

- Admins join and leave from the console. The CLI stays for the first
  owner.
- **No email yet:** the console shows the link once to copy and send.
  Sending it by email needs the admin module to queue a job (no external
  call in a request); that is left for later.
- A reset link is as sensitive as a password until it is used: it is shown
  once, expires in 72 hours, and works once.
