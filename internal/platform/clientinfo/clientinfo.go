// Package clientinfo reduces what we keep about a client device to what
// sessions and audit logs need, without storing personal identifiers.
package clientinfo

import "net/netip"

// IPPrefix reduces an address to its /24 (IPv4) or /48 (IPv6) network:
// enough to recognise "same network" in a session list or audit entry,
// without storing a personal identifier.
func IPPrefix(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	a = a.Unmap()
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

// TruncateUserAgent bounds what we keep of a User-Agent header.
func TruncateUserAgent(ua string) string {
	const maxLen = 200
	if len(ua) <= maxLen {
		return ua
	}
	// Cut on a rune boundary.
	for i := maxLen; i > 0; i-- {
		if ua[i]&0xC0 != 0x80 {
			return ua[:i]
		}
	}
	return ""
}
