// Package csp builds the Content-Security-Policy headers (CLAUDE.md
// security rules). The middleware sets the defaults; the storefront adds
// the hash of each page's inline stylesheet.
package csp

import "strings"

// API is the policy for JSON APIs: nothing may render, frame or load.
const API = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Storefront is the storefront's policy. Scripts come only from our
// origin. Styles too, plus the inline stylesheets named by styleHashes
// ('sha256-…'): pages inline their critical CSS, and a hash (not a nonce)
// keeps them cacheable, since the same page always has the same hash
// (ADR-0018).
func Storefront(styleHashes ...string) string {
	style := "style-src 'self'"
	for _, h := range styleHashes {
		style += " " + h
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		style,
		"img-src 'self' https: data:",
		"font-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"upgrade-insecure-requests",
	}, "; ")
}
