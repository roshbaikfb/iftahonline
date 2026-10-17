// Package domain is alpha's domain fixture.
package domain

import "strings"

// Normalize trims and lower-cases a name.
func Normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
