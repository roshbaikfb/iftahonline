# ADR-0032: Section types are an extensible list

- Status: Accepted
- Date: 2026-10-24

## Context

The storefront builds its pages from sections (ADR-0018). The contract
lists their types in `SectionType`, a closed `enum`. That enum appears
in responses: the home page, landing pages, policies and
`section-types`.

U2a adds three types from the approved design:

- `category_tiles`;
- `promo_tiles`;
- `whatsapp_cta`.

More will follow as the design work goes on.

The OpenAPI gate (`make openapi-diff`, oasdiff) flags every new value of
a response enum as breaking. A client written against the old list may
receive a value it does not know. CLAUDE.md rule 11 allows no breaking
changes inside /v1.

We tried two ways to declare the list open, and neither passes the
gate:

- **Replacing `enum` with `x-extensible-enum`.** It is reported as
  removing every value from the requests.
- **Keeping `enum` and adding `x-extensible-enum`.** The same error
  stays.

Waiting for /v2 is not an option either: a merchant needs to see and add
the new sections now.

Today the only client of these endpoints is our dashboard. It is
generated from the same file, in the same commit, and it does not read
sections yet.

## Decision

1. **`SectionType` is an extensible list.** Its description in
   `api/openapi.yaml` says so: new types are added within /v1. Clients
   must accept a type they do not know, by skipping that section or
   showing it as unsupported, never by failing. The dashboard's section
   editor, when it is built, must do the same.
2. **Exceptions are listed one by one.** Each new value is accepted as
   an exact oasdiff finding (method, path and message) in
   `api/openapi-breaking-ignore.txt`, which `scripts/openapi-diff.sh`
   passes as `--err-ignore`. Every line names its value, so:
   - a value nobody approved still fails the gate;
   - any other kind of break still fails the gate.
3. **The file holds only this.** It never takes anything that is not a
   new value of an extensible list. Adding a line is a reviewed change
   that cites this ADR.

We checked the gate stays strict by testing two breaks against the
U2a list:

- a fourth, unlisted type (`video`) fails;
- making the `definitions` response property optional fails.

## Consequences

- **New section types ship within /v1.** Each one adds its lines to the
  exception file in the same PR, with the type's schema and renderer.
- **Clients take on a duty.** They must tolerate unknown types; the
  contract says so. Our generated TypeScript union still lists the
  known types, so code that switches on it needs a default branch.
- **Stale lines are harmless.** Once main has a value, its lines match
  nothing. The file can be emptied whenever it is convenient.
- **Requests are unaffected.** A new value in a request is not a
  breaking change, and the server still rejects types it does not know
  (`sections.Decode`).
