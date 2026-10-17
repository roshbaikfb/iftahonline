# Gate fixtures

`scripts/gatecheck.sh` copies `base/` plus one scenario into a temporary
module that uses the real module path and the real `.golangci.yml`. It
asserts that:

- `base/` alone builds and lints clean (control);
- `internal_import/` fails `go build` (Go `internal/` rule, ADR-0001);
- `dupl/` fails golangci-lint with `dupl` (40-line duplicated block);
- `depguard/` fails golangci-lint with `depguard` (domain imports net/http).

Go tooling ignores `testdata/`, so these files never build as part of the
real module.
