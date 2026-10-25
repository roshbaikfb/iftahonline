# Module: onboarding

The onboarding module owns the new store's **setup wizard**: what the
merchant sells (the activity), the storefront's look and main colour,
and the **starter template** applied from that choice. Design:
[ADR-0034](../adr/0034-setup-wizard-and-starter-templates.md).

## Public API (`internal/modules/onboarding/api.go`)

| Symbol | Purpose |
|---|---|
| `New(Deps)`, `Module.Mount`, `Module.RegisterWorkers` | Wiring, the merchant HTTP API (behind identity's membership middleware), and the apply job's worker |
| `Module.Starter()` → `Starter` | What sign-up (identity) uses: `StartTx(ctx, tx)` opens the wizard of the new store in sign-up's transaction |
| `Choice`, `Activity`, `Look` | The merchant's pick |
| `Templates` | `Apply(ctx, choice) (version, error)`: the port the worker applies templates through. Until ADR-0034's second step it is a no-op, so a choice becomes `ready` with no version |

## Events

None. The apply job is a River job enqueued with the choice
(`onboarding.apply_template`, ADR-0003).

## Tables

| Table | Notes |
|---|---|
| `store_onboarding` | Tenant table under RLS, one row per store made since the wizard (PK `store_id`). Stores made before it have no row and read as `completed`. `template_seq` counts choices; see the rules below |

## HTTP API (tag `onboarding`)

| Operation | Permission |
|---|---|
| `getOnboarding` | `store.read` |
| `chooseOnboardingTemplate` (202; 409 `onboarding_completed` once closed) | `store.update` |
| `completeOnboarding` (idempotent) | `store.update` |

## Rules worth knowing

- **The last choice wins.** Each choice increments `template_seq` and
  enqueues a job carrying the new value. Jobs of one store take an
  advisory transaction lock, so they run one at a time; a job whose
  value is no longer current does nothing. Choosing never waits on the
  lock.
- **Failures are shown, not hidden.** A job retries 5 times; the last
  attempt records `failed`, and the wizard offers to try again (choosing
  again enqueues a new job).
- **Audit.** `onboarding.template_chosen` (activity, look, colour) and
  `onboarding.completed`.
