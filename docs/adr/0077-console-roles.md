# ADR-0077: Console roles

- Status: Accepted
- Date: 2026-11-03

## Context

Until now every console admin could do everything (ADR-0010 left
"per-admin roles" for later). The console now holds money actions:
settling invoices, recording refunds and exporting invoices. The owner
approved the plan on 2026-11-03. It covers roles, enforced by the server,
and a team page with invite links, which comes in a second step.

## Decision

### Five roles

Each admin has exactly one role (`platform_admins.role`, migration
00091). Every existing admin became an owner, since each could already
do everything.

| Role | May |
|---|---|
| `owner` | Everything, including the team and the admin log |
| `operations` | Stores: suspend, reactivate, support sessions, feature overrides; reads a store's billing |
| `finance` | Invoices and payments, settling, refunds, the CSV export; plans, features, founding, agencies |
| `content` | The site, guides and ads |
| `viewer` | Reads stores, billing, the catalog and content; does nothing |

- **Permissions:** a role is a fixed set of permissions (`domain.Role.Can`):
  `stores.read`/`manage`, `contacts.read`, `billing.read`/`manage`,
  `catalog.read`/`manage`, `content.read`/`manage`, `audit.read` and
  `team.manage`. These are areas with a read and a manage level. There is
  no permission per endpoint, so a new endpoint fits into a role without a
  new decision.
- **Contacts:** a store owner's phone and email (ADR-0075's store page)
  need `contacts.read`: owners and operations.
- **The admin log** (`audit.read`) is for owners only.
- **The home page** is for every admin. A section the role may not read is
  not requested.

### Enforced by the server, in one place

- **The middleware:** a strict-handler middleware (`authorize`) looks the
  operation up in one table (`httpapi/permissions.go`) before its handler
  runs. Anything not granted gets a 403.
- **Fail closed:** an operation missing from the table is refused for
  everyone.
- **The guard test:** `TestEveryOperationHasAPermission` fails when a new
  operation is not in the table, or when the table names an operation that
  is gone. It also fails when an operation that changes something (by its
  verb) needs only a read permission.
- **Contacts:** the store-owner contact rule lives in the app
  (`StoreProfile` drops the owner), because one endpoint serves both kinds
  of reader.
- **`/admin/v1/me`** returns the role and its permissions. The console
  hides what the role lacks:
  - its sections in the menu;
  - a page opened by its address, which says so;
  - each action button;
  - the forms of an editor page, which show disabled under «عرض فقط».

  Hiding is for comfort. The server is the wall.

### Creating admins

- **The CLI:** `platform admin create` takes `--role`, which defaults to
  `owner` because the first admin runs the team.
- **The team page:** inviting admins, changing roles, disabling and
  resetting 2FA from the console come with the team page (the second step
  of this plan).
- **Until then:** roles change in SQL, as disabling does today.

## Consequences

- A finance or content hire no longer needs full power.
- Every new console endpoint must name its permission, or the build's tests
  fail.
- Roles are fixed in code. A custom role would need a new decision and a
  table of its own.
