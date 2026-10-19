// Package email validates and normalizes email addresses taken from
// users (merchant accounts, invitations, platform admins).
package email

import (
	"net/mail"
	"strings"
)

// Normalize lower-cases and validates an address. Display names,
// comments and IP-literal domains are rejected.
func Normalize(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) < 3 || len(s) > 254 || strings.ContainsAny(s, " <>\"()[],;\\") {
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", false
	}
	local, host, ok := strings.Cut(s, "@")
	if !ok || local == "" || len(local) > 64 || !strings.Contains(host, ".") ||
		strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", false
	}
	return s, true
}
