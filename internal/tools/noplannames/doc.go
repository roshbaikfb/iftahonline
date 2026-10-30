// Package noplannames holds the test that keeps plan names out of every
// decision outside the billing module (P7 acceptance, ADR-0045). Code
// asks the entitlements module whether a store has a feature; it never
// asks which plan the store is on, so a plan can be renamed, repriced or
// regranted as data without touching code.
//
// The test reads the plan codes from the migrations that seed them and
// fails on, outside internal/modules/billing:
//   - a Go comparison (==, !=, a switch case, strings.EqualFold and the
//     like) with a string literal that is a plan code, or of a value
//     named for a plan's code or name (planCode, PlanName, ...);
//   - a dashboard (TypeScript) comparison that involves a plan and a
//     plan code, or a plan's code or name.
//
// It runs in `make test`, so it fails `make ci`. A line that compares
// such a literal for another reason (an order's source is "merchant")
// ends with a "noplannames:allow <reason>" comment, which a reviewer
// then sees.
package noplannames
