// Package nocarddata holds the test that keeps card data out of the
// code base (CLAUDE.md security rules, ADR-0024): shoppers pay on the
// gateway's hosted page, so no field, column, form input or API property
// may ever be named for a card number, security code, expiry or track
// data. The test runs in `make test`, so it fails `make ci`.
//
// A line that must mention such a name (a provider's signed field list
// carrying a masked value, say) ends with a "nocarddata:allow <reason>"
// comment, which a reviewer then sees.
package nocarddata
