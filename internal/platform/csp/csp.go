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
	return StorefrontWith(Sources{}, styleHashes...)
}

// turnstile is Cloudflare Turnstile's origin.
const turnstile = "https://challenges.cloudflare.com"

// StorefrontChallenge is Storefront plus Cloudflare Turnstile's script
// and frame, for the one private response that shows a challenge
// (ADR-0022). Cached pages never carry it.
func StorefrontChallenge(styleHashes ...string) string {
	return StorefrontWith(Sources{Script: []string{turnstile}, Frame: []string{turnstile}}, styleHashes...)
}

// Sources are third-party origins a page may load scripts from, send
// requests to and frame: a store's ad pixels (ADR-0043), or Turnstile.
type Sources struct {
	Script, Connect, Frame []string
}

// StorefrontWith is Storefront plus src. Each source must be a plain
// https origin (wildcard host allowed); anything else is dropped, so a
// source can never end a directive or add a keyword.
func StorefrontWith(src Sources, styleHashes ...string) string {
	style := "style-src 'self'"
	for _, h := range styleHashes {
		style += " " + h
	}
	directives := []string{}
	if frame := origins(src.Frame); frame != "" {
		directives = append(directives, "frame-src"+frame)
	}
	return strings.Join(append([]string{
		"default-src 'self'",
		"script-src 'self'" + origins(src.Script),
		style,
		"img-src 'self' https: data:",
		"font-src 'self'",
		"connect-src 'self'" + origins(src.Connect),
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"upgrade-insecure-requests",
	}, directives...), "; ")
}

// origins joins the plain https origins of list, each after a space.
func origins(list []string) string {
	var b strings.Builder
	for _, o := range list {
		if plainOrigin(o) {
			b.WriteString(" " + o)
		}
	}
	return b.String()
}

// plainOrigin reports whether o is "https://" and a host of letters,
// digits, dots and dashes, optionally starting with "*.".
func plainOrigin(o string) bool {
	host, ok := strings.CutPrefix(o, "https://")
	host = strings.TrimPrefix(host, "*.")
	if !ok || host == "" {
		return false
	}
	for _, r := range host {
		if ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-'; !ok {
			return false
		}
	}
	return true
}
