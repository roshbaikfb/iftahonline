# ADR-0001: Go modular monolith, one binary, on Linux

- Status: Accepted
- Date: 2026-10-17

## Context

We are building a multi-tenant e-commerce SaaS for Egypt and the Gulf.
Target: 5,000+ active stores, 500,000+ shoppers, 3,000 rps storefront peak
(about 90% from the edge cache). The team is small. We need fast iteration
and clear module boundaries that can later be split if one area outgrows the
rest. Running microservices from day one would cost us distributed
transactions, network failure modes and heavy operational overhead before
we have any revenue.

## Decision

1. **A single Go module and a single binary, `platform`.** It has the
   subcommands `api`, `storefront`, `worker` and `migrate`. Each role scales
   and deploys independently (systemd unit or container) but ships from one
   artifact.
2. **A modular monolith.** Each business module lives in
   `internal/modules/<name>/`, laid out like this:
   - `api.go` is the only exported surface (interfaces and DTOs);
   - `module.go` holds the constructor;
   - `internal/{domain,app,store,httpapi}` holds the implementation.

   Go's `internal/` rule makes it a compile error to import another
   module's internals. Modules talk only through `api.go` interfaces or
   domain events.
3. **`domain/` imports only the standard library and `internal/platform`.**
   depguard enforces this in `.golangci.yml`.
4. **Shared, module-agnostic code lives in `internal/platform`,** and only
   once two or more modules use it. Process wiring (router, middleware,
   health, lifecycle) lives in `internal/server`.
5. **Linux is the only target.** We build with `CGO_ENABLED=0 -trimpath`
   and ship either a distroless/static nonroot image or a bare binary under
   hardened systemd units. Config comes only from environment variables,
   logs are JSON on stdout, and every role shuts down gracefully on
   SIGTERM.

## Consequences

- **Positive:**
  - Function calls between modules instead of network calls.
  - One CI pipeline and one release artifact.
  - A refactor across modules is a single commit.
  - The boundaries are checked at compile time and by the linter, not by
    convention.
- **Negative:**
  - All roles share one dependency graph and one Go version.
  - A bad change can affect every role, so CI quality gates matter more.
- **Exit path:** a module whose only coupling is `api.go` plus events can
  be extracted into its own service by putting its interface behind
  HTTP/gRPC and its events on a broker.
